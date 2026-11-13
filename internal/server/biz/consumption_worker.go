package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	"github.com/google/uuid"

	stdsql "database/sql"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/consumptionsnapshot"
	"github.com/looplj/axonhub/internal/log"
)

// 每次只领取一个最近仍被查看的日期，租约跨进程互斥并在崩溃后自动到期。

// 请求断开不销毁回填进度；停止查看五秒后不再领取该范围的未完成任务。

func (s *ConsumptionService) fillConsumptionSnapshot(ctx context.Context) error {
	if !s.worker.TryLock() {
		return nil
	}

	defer s.worker.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)

	defer cancel()

	_, err := authz.RunAsHost(ctx, s.client, "consumption-backfill", func(ctx context.Context) (bool, error) {
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

			// 计算超时后仍用有界维护写入记录失败，不能沿用已取消的期限。

			failureCtx, failureCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)

			defer failureCancel()

			_, saveErr := s.client.ConsumptionSnapshot.Update().Where(consumptionsnapshot.IDEQ(job.ID), consumptionsnapshot.LeaseTokenEQ(token)).
				SetFailed(true).SetLeaseUntil(time.Now().UTC().Add(30 * time.Second)).Save(failureCtx)

			if saveErr != nil {
				return false, saveErr
			}

			return false, err

		}

		if result == nil {

			count, err = s.client.ConsumptionSnapshot.Update().Where(consumptionsnapshot.IDEQ(job.ID), consumptionsnapshot.LeaseTokenEQ(token), consumptionsnapshot.SourceVersionEQ(job.SourceVersion)).
				SetSnapshotVersion(job.SourceVersion).SetPending(false).SetFailed(false).SetCheckedAt(time.Now().UTC()).SetLeaseUntil(time.Unix(0, 0).UTC()).Save(ctx)

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
			SetPayload(bytes).SetFingerprint(fingerprint).SetCheckedAt(time.Now().UTC()).SetSnapshotVersion(job.SourceVersion).SetGeneratedAt(result.GeneratedAt).SetPending(false).SetFailed(false).SetLeaseUntil(time.Unix(0, 0).UTC()).Save(ctx)
		if err != nil {
			return false, err
		}

		if count == 0 {

			q := s.client.ConsumptionSnapshot.Update().Where(consumptionsnapshot.IDEQ(job.ID), consumptionsnapshot.LeaseTokenEQ(token))

			if job.RangeEnd.After(now) {
				// 活跃日持续写入也必须能展示已完成的一致读快照；一分钟内保留

				// 此计算时间，仍标记待更新。历史修正则必须等对应新版本完成。

				q.SetPayload(bytes).SetFingerprint(fingerprint).SetSnapshotVersion(job.SourceVersion).SetGeneratedAt(result.GeneratedAt).SetCheckedAt(result.GeneratedAt).SetLeaseUntil(time.Now().UTC().Add(time.Minute))
			} else {
				q.SetLeaseUntil(time.Unix(0, 0).UTC())
			}

			_, err = q.Save(ctx)

		}

		return count > 0, err
	})

	return err
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

	opts := &stdsql.TxOptions{ReadOnly: true, Isolation: stdsql.LevelRepeatableRead}

	if s.client.Driver().Dialect() == dialect.SQLite {
		opts = &stdsql.TxOptions{}
	}

	tx, err := s.client.BeginTx(ctx, opts)
	if err != nil {
		return nil, "", err
	}

	defer func() { _ = tx.Rollback() }()

	reader := *s

	reader.client = tx.Client()

	fingerprint, err := reader.consumptionFingerprint(ctx, query)

	if err != nil || (len(job.Payload) > 0 && fingerprint == job.Fingerprint) {
		return nil, fingerprint, err
	}

	result, err := reader.query(ctx, query.Filter, "snapshot", query.Project)

	if err == nil && (result.Timezone != query.Timezone || result.CurrencyCode != query.Currency || result.RangeStart == nil || !result.RangeStart.Equal(query.Start) || !result.RangeEnd.Equal(query.End)) {
		return nil, "", fmt.Errorf("consumption summary configuration changed during calculation")
	}

	return result, fingerprint, err
}
