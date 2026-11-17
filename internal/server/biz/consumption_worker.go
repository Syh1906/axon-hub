package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

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

// Drain available dates without inserting a scheduler interval between them.
// The existing worker lock/leases still bound concurrency to one computation.
func (s *ConsumptionService) fillRequestedConsumptionSnapshots(ctx context.Context) error {
	until := time.Now().Add(25 * time.Second)
	for range consumptionSlicePageDays {
		if time.Now().After(until) {
			return nil
		}
		filled, err := s.fillNextConsumptionSnapshot(ctx)
		if err != nil || !filled {
			return err
		}
	}
	return nil
}

func (s *ConsumptionService) fillNextConsumptionSnapshot(ctx context.Context) (bool, error) {
	if s.demand != nil && time.Now().UnixNano() > s.demand.Load() {
		return false, nil
	}
	if !s.worker.TryLock() {
		return false, nil
	}

	defer s.worker.Unlock()

	parent := ctx
	budgetDeadline := time.Now().Add(25 * time.Second)
	ctx, cancel := context.WithDeadline(ctx, budgetDeadline)

	defer cancel()

	return authz.RunAsHost(ctx, s.client, "consumption-backfill", func(ctx context.Context) (filled bool, runErr error) {
		ctx = contexts.WithPrimaryRead(ctx)

		now := time.Now().UTC()

		job, err := s.client.ConsumptionSnapshot.Query().Where(consumptionsnapshot.PendingEQ(true), consumptionsnapshot.LeaseUntilLTE(now), consumptionsnapshot.RequestedAtGTE(now.Add(-5*time.Second))).
			Order(ent.Desc(consumptionsnapshot.FieldRequestedAt), ent.Asc(consumptionsnapshot.FieldID)).First(ctx)

		if ent.IsNotFound(err) {
			return false, nil
		}

		if err != nil {
			return false, err
		}

		token := uuid.NewString()

		count, err := s.client.ConsumptionSnapshot.Update().Where(consumptionsnapshot.IDEQ(job.ID), consumptionsnapshot.PendingEQ(true), consumptionsnapshot.LeaseUntilLTE(now)).
			SetLeaseToken(token).SetLeaseUntil(now.Add(time.Minute)).SetFailed(false).Save(ctx)

		if err != nil || count == 0 {
			return false, err
		}

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

			if query.Version != consumptionSnapshotVersion || s.system.TimeLocation(ctx).String() != query.Timezone {
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

func (s *ConsumptionService) expireConsumptionSnapshots(ctx context.Context) {
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
