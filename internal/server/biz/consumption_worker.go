package biz

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/consumptionsnapshot"
	"github.com/looplj/axonhub/internal/log"
)

// 每次只领取一个最近仍被查看的日期，租约跨进程互斥并在崩溃后自动到期。

// 请求断开不销毁回填进度；停止查看五秒后不再领取该范围的未完成任务。

func (s *ConsumptionService) fillConsumptionSnapshot(ctx context.Context) error {
	_, err := s.fillNextConsumptionSnapshot(ctx)
	return err
}

// 数据库繁忙时只完成已领取的任务，空闲时按批增加同时处理的日期。
func (s *ConsumptionService) fillRequestedConsumptionSnapshots(ctx context.Context) error {
	if s.demand != nil && time.Now().UnixNano() > s.demand.Load() {
		return nil
	}
	if !s.worker.TryLock() {
		return nil
	}
	defer s.worker.Unlock()
	until := time.Now().Add(25 * time.Second)
	limit := 1
	previous := s.primaryPoolStats()
	for remaining := consumptionSlicePageDays; remaining > 0 && time.Now().Before(until); {
		if s.demand != nil && time.Now().UnixNano() > s.demand.Load() {
			return nil
		}
		current := s.primaryPoolStats()
		limit = consumptionWorkerLimit(s.client.Driver().Dialect(), limit, previous, current)
		limit = s.foreground.limit(limit, time.Now(), runtime.GOMAXPROCS(0))

		if limit == 0 {
			return nil
		}
		previous = current
		var group errgroup.Group
		var mu sync.Mutex
		completed := 0
		run := func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("consumption worker panic: %v", r)
					log.Warn(ctx, "fill consumption date", log.Cause(err))
				}
			}()
			ok, err := s.fillConsumptionDate(ctx, limit)
			if ok {
				mu.Lock()
				completed++
				mu.Unlock()
			}
			return err
		}
		for range min(remaining, limit) {
			group.Go(run)
		}
		if err := group.Wait(); err != nil || completed == 0 {
			return err
		}
		remaining -= completed
		if completed == limit {
			limit++
		}
	}
	return nil
}

// 采样正常入账的本地事务耗时，避开上游模型响应耗时。
type consumptionWritePressure struct {
	samples    atomic.Uint64
	usual      atomic.Int64
	yieldUntil atomic.Int64
	lastWrite  atomic.Int64
}

func (p *consumptionWritePressure) observe(start time.Time) {
	now := time.Now()
	p.lastWrite.Store(now.UnixNano())
	elapsed := now.Sub(start)
	base := time.Duration(p.usual.Load())
	if base == 0 {
		p.usual.CompareAndSwap(0, int64(elapsed))
		return
	}
	if elapsed > 3*base && elapsed-base > 10*time.Millisecond {
		p.yieldUntil.Store(time.Now().Add(2 * time.Second).UnixNano())
	}
	p.usual.CompareAndSwap(int64(base), int64(base+(elapsed-base)/16))
}

func (p *consumptionWritePressure) limit(desired int, now time.Time, cpus int) int {
	if now.UnixNano() < p.yieldUntil.Load() {
		return min(desired, 1)
	}
	// 入账活跃时至少保留一半 CPU；连接充足也可能争用本地计算资源。
	if now.Add(-2*time.Second).UnixNano() < p.lastWrite.Load() {
		return min(desired, max(1, cpus/2))
	}
	return desired
}

func (s *ConsumptionService) primaryPoolStats() sql.DBStats {
	switch driver := s.client.Driver().(type) {
	case interface{ PrimaryStats() sql.DBStats }:
		return driver.PrimaryStats()
	case *entsql.Driver:
		return driver.DB().Stats()
	default:
		return sql.DBStats{}
	}
}

func consumptionWorkerLimit(db string, desired int, last, now sql.DBStats) int {
	maximum := now.MaxOpenConnections
	if maximum > 0 && now.InUse >= maximum {
		return 0
	}
	if db == dialect.SQLite || maximum == 0 || now.WaitCount > last.WaitCount || now.InUse*2 >= maximum {
		return 1
	}
	// 为转发、入账与管理请求保留连接。统计并发也受进程可用 CPU 数约束。
	return min(desired, max(1, maximum/4), runtime.GOMAXPROCS(0))
}

func (s *ConsumptionService) fillNextConsumptionSnapshot(ctx context.Context) (bool, error) {
	if !s.worker.TryLock() {
		return false, nil
	}
	defer s.worker.Unlock()
	return s.fillConsumptionDate(ctx, 1)
}

func (s *ConsumptionService) fillConsumptionDate(ctx context.Context, limit int) (bool, error) {
	if s.demand != nil && time.Now().UnixNano() > s.demand.Load() {
		return false, nil
	}
	parent := ctx
	budgetDeadline := time.Now().Add(25 * time.Second)
	ctx, cancel := context.WithDeadline(ctx, budgetDeadline)

	defer cancel()

	return authz.RunAsHost(ctx, s.client, "consumption-backfill", func(ctx context.Context) (filled bool, runErr error) {
		ctx = contexts.WithPrimaryRead(ctx)

		now := time.Now().UTC()

		token := uuid.NewString()
		job, err := s.claimConsumptionDate(ctx, now, token, limit)
		if err != nil || job == nil {
			return false, err
		}
		defer s.ready.published(job.CacheKey)
		var count int

		// Every error after claiming the job, including publication, must release
		// or cool down this lease. Maintenance never publishes a canceled payload.
		defer func() {
			if runErr == nil {
				return
			}
			filled = false
			failureCtx, failureCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer failureCancel()
			// RunAsHost can report Canceled when its callback wins the deadline
			// timer. Classify against our deadline, not timer callback order.
			interrupted := errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded)
			exhausted := interrupted && !time.Now().Before(budgetDeadline) && parent.Err() == nil
			until := time.Now().UTC().Add(30 * time.Second)
			if exhausted {
				until = time.Unix(0, 0).UTC()
			}
			_, saveErr := s.client.ConsumptionSnapshot.Update().Where(consumptionsnapshot.IDEQ(job.ID), consumptionsnapshot.LeaseTokenEQ(token)).
				SetFailed(!exhausted).SetLeaseUntil(until).Save(failureCtx)
			if saveErr != nil {
				runErr = errors.Join(runErr, saveErr)
			} else if exhausted {
				runErr = nil
			}
		}()

		var query consumptionDayQuery

		if err = json.Unmarshal(job.Query, &query); err == nil {

			query.Filter.Uncached, query.Filter.DailySnapshot = true, true

			if query.Version != consumptionVersion(query.Filter) || s.system.TimeLocation(ctx).String() != query.Timezone {
				err = fmt.Errorf("consumption summary configuration changed")
			}

			settings, settingsErr := s.system.GeneralSettings(ctx)

			if settingsErr != nil {
				err = settingsErr
			} else if settings.CurrencyCode != query.Currency {
				err = fmt.Errorf("consumption currency changed")
			}

		}

		var result *ConsumptionResult

		fingerprint := ""

		if err == nil {
			result, fingerprint, err = s.readConsumptionDay(ctx, query, job)
		}

		if err != nil {
			return false, err
		}

		if result == nil {

			count, err = s.client.ConsumptionSnapshot.Update().Where(consumptionsnapshot.IDEQ(job.ID), consumptionsnapshot.LeaseTokenEQ(token), consumptionsnapshot.SourceVersionEQ(job.SourceVersion)).
				SetSourceVersion(fingerprint).SetSnapshotVersion(fingerprint).SetPending(false).SetFailed(false).SetCheckedAt(time.Now().UTC()).SetLeaseUntil(time.Unix(0, 0).UTC()).Save(ctx)

			if err == nil && count == 0 {
				_, err = s.client.ConsumptionSnapshot.Update().Where(consumptionsnapshot.IDEQ(job.ID), consumptionsnapshot.LeaseTokenEQ(token)).SetLeaseUntil(time.Unix(0, 0).UTC()).Save(ctx)
			}

			return count > 0, err

		}

		result.Revision = token

		bytes, err := json.Marshal(result)
		if err != nil {
			return false, err
		}

		// 事实版本若在生成期间变化，不发布旧数据，也不覆盖新版本的 pending。

		count, err = s.client.ConsumptionSnapshot.Update().Where(consumptionsnapshot.IDEQ(job.ID), consumptionsnapshot.LeaseTokenEQ(token), consumptionsnapshot.SourceVersionEQ(job.SourceVersion)).
			SetPayload(bytes).SetFingerprint(fingerprint).SetCheckedAt(time.Now().UTC()).SetSourceVersion(fingerprint).SetSnapshotVersion(fingerprint).SetGeneratedAt(result.GeneratedAt).SetPending(false).SetFailed(false).SetLeaseUntil(time.Unix(0, 0).UTC()).Save(ctx)
		if err != nil {
			return false, err
		}

		if count == 0 {

			q := s.client.ConsumptionSnapshot.Update().Where(consumptionsnapshot.IDEQ(job.ID), consumptionsnapshot.LeaseTokenEQ(token))

			if job.RangeEnd.After(now) {
				// 活跃日持续写入也可展示本次刷新开始之后完成的一致快照。

				// 保留待更新版本；下一次明确刷新无需等待固定租约。

				q.SetPayload(bytes).SetFingerprint(fingerprint).SetSnapshotVersion(fingerprint).SetGeneratedAt(result.GeneratedAt).SetCheckedAt(result.GeneratedAt).SetLeaseUntil(time.Unix(0, 0).UTC())
			} else {
				q.SetLeaseUntil(time.Unix(0, 0).UTC())
			}

			_, err = q.Save(ctx)

		}

		return count > 0, err
	})

}

// 领取阶段只短暂锁住同一条记录；计算期间只保留日期租约。
func (s *ConsumptionService) claimConsumptionDate(ctx context.Context, now time.Time, token string, limit int) (*ent.ConsumptionSnapshot, error) {
	opts := &sql.TxOptions{Isolation: sql.LevelReadCommitted}
	shared := s.client.Driver().Dialect() != dialect.SQLite
	if !shared {
		opts = &sql.TxOptions{}
	}
	tx, err := s.client.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if shared {
		// 各实例在相同的短事务内排队领取，避免它们同时使用最后一个空位。
		_, err = tx.ConsumptionSnapshot.Query().Order(ent.Asc(consumptionsnapshot.FieldID)).Limit(1).
			Modify(func(q *entsql.Selector) { q.Select(q.C(consumptionsnapshot.FieldID)).ForUpdate() }).Int(ctx)
		if ent.IsNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		running, err := tx.ConsumptionSnapshot.Query().Where(consumptionsnapshot.Pending(true), consumptionsnapshot.Failed(false), consumptionsnapshot.LeaseUntilGT(now)).Count(ctx)
		if err != nil || running >= limit {
			return nil, err
		}
	}
	job, err := tx.ConsumptionSnapshot.Query().Where(consumptionsnapshot.Pending(true), consumptionsnapshot.LeaseUntilLTE(now), consumptionsnapshot.RequestedAtGTE(now.Add(-5*time.Second))).
		Order(ent.Desc(consumptionsnapshot.FieldRequestedAt), ent.Desc(consumptionsnapshot.FieldRangeStart), ent.Asc(consumptionsnapshot.FieldID)).First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	count, err := tx.ConsumptionSnapshot.Update().Where(consumptionsnapshot.IDEQ(job.ID), consumptionsnapshot.Pending(true), consumptionsnapshot.LeaseUntilLTE(now)).
		SetLeaseToken(token).SetLeaseUntil(now.Add(time.Minute)).SetFailed(false).Save(ctx)
	if err != nil || count == 0 {
		return nil, err
	}
	return job, tx.Commit()
}

func (s *ConsumptionService) expireConsumptionSnapshots(ctx context.Context) {
	for _, key := range s.rankings.Keys() {
		if value, ok := s.rankings.Peek(key); ok && time.Now().After(value.until) {
			s.rankings.Remove(key)
		}
	}

	_, err := authz.RunAsHost(ctx, s.client, "consumption-snapshot-expiry", func(ctx context.Context) (int, error) {
		// 清理按最后请求而非生成时间；epoch/正在生成的任务不会被误删。

		return s.client.ConsumptionSnapshot.Delete().Where(consumptionsnapshot.RequestedAtLT(time.Now().AddDate(0, 0, -30)), consumptionsnapshot.LeaseUntilLT(time.Now())).Exec(ctx)
	})
	if err != nil {
		log.Warn(ctx, "expire consumption snapshots", log.Cause(err))
	}
}

// 指纹、账目、性能与名称来自同一读取快照，避免把旧签名绑定到新数据。

func (s *ConsumptionService) readConsumptionDay(ctx context.Context, query consumptionDayQuery, job *ent.ConsumptionSnapshot) (*ConsumptionResult, string, error) {
	query.Filter.Uncached, query.Filter.DailySnapshot = true, true
	if len(job.Payload) == 0 {
		if result, version, err := s.emptyConsumptionDay(ctx, query); err != nil || result != nil {
			return result, version, err
		}
	}
	// Commit reusable hours separately. A later hour's cancellation must not
	// discard already completed historical aggregates.
	if err := s.prepareConsumptionHours(ctx, query, job); err != nil {
		return nil, "", err
	}

	var fingerprint string
	result, err := readStatisticsSnapshot(ctx, s.client, false, func(client *ent.Client) (*ConsumptionResult, error) {
		started := time.Now().UTC()
		reader := *s
		reader.client = client
		var err error
		fingerprint, err = reader.consumptionFingerprint(ctx, query)
		if err != nil || (len(job.Payload) > 0 && fingerprint == job.Fingerprint) {
			return nil, err
		}
		facts, err := reader.consumptionHourlyFacts(ctx, query)
		if err != nil {
			return nil, err
		}
		result, err := reader.queryWithFacts(ctx, query.Filter, "snapshot", query.Project, facts)
		if err != nil {
			return nil, err
		}
		if result.Timezone != query.Timezone || result.CurrencyCode != query.Currency || result.RangeStart == nil || !result.RangeStart.Equal(query.Start) || !result.RangeEnd.Equal(query.End) {
			return nil, fmt.Errorf("consumption summary configuration changed during calculation")
		}
		result.GeneratedAt = started
		return result, nil
	})
	return result, fingerprint, err
}
