package biz

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/stretchr/testify/require"
)

type fingerprintCaptureDriver struct {
	dialect.Driver
	queries []string
}

func (*fingerprintCaptureDriver) Dialect() string { return dialect.Postgres }
func (d *fingerprintCaptureDriver) Query(ctx context.Context, query string, args, rows any) error {
	d.queries = append(d.queries, query)
	return d.Driver.Query(ctx, "SELECT id FROM requests WHERE 1=0", []any{}, rows)
}

func TestConsumptionFingerprintPostgresIdentifiers(t *testing.T) {
	f := newConsumptionFixture(t)
	driver := &fingerprintCaptureDriver{Driver: f.client.Driver()}
	reader := *f.service
	reader.client = ent.NewClient(ent.Driver(driver))
	_, err := reader.consumptionFingerprint(f.ctx, consumptionDayQuery{Project: f.project.ID, Start: time.Now().Add(-time.Hour), End: time.Now(), Filter: ConsumptionFilter{Dimension: ConsumptionDimensionChannel}})
	require.NoError(t, err)
	require.Len(t, driver.queries, 11)
	for _, query := range driver.queries {
		require.NotContains(t, query, "`")
	}
}

type fingerprintTransactionDriver struct {
	dialect.Driver
	begins, outsideReads int
}

func (d *fingerprintTransactionDriver) Query(ctx context.Context, query string, args, rows any) error {
	d.outsideReads++
	return d.Driver.Query(ctx, query, args, rows)
}
func (d *fingerprintTransactionDriver) BeginTx(ctx context.Context, opts *stdsql.TxOptions) (dialect.Tx, error) {
	d.begins++
	return d.Driver.(interface {
		BeginTx(context.Context, *stdsql.TxOptions) (dialect.Tx, error)
	}).BeginTx(ctx, opts)
}

func TestConsumptionFingerprintAndPayloadShareTransaction(t *testing.T) {
	f := newConsumptionFixture(t)
	at := time.Now().AddDate(0, 0, -3)
	f.addRequest(t, at, "model-a", request.StatusCompleted, 100, 10, 0, 0, 2, nil)
	loc := f.service.system.TimeLocation(f.ctx)
	start := time.Date(at.In(loc).Year(), at.In(loc).Month(), at.In(loc).Day(), 0, 0, 0, 0, loc)
	input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodCustom, OmitPerformance: true}
	_, bytes, err := consumptionDayKey(f.project.ID, input, start, start.AddDate(0, 0, 1), "USD")
	require.NoError(t, err)
	var query consumptionDayQuery
	require.NoError(t, json.Unmarshal(bytes, &query))
	driver := &fingerprintTransactionDriver{Driver: f.client.Driver()}
	reader := *f.service
	reader.client = ent.NewClient(ent.Driver(driver))
	result, fingerprint, err := reader.readConsumptionDay(f.ctx, query, &ent.ConsumptionSnapshot{})
	require.NoError(t, err)
	require.Equal(t, 2.0, result.Entries[0].Metrics.TotalCost)
	require.NotEmpty(t, fingerprint)
	require.Equal(t, 1, driver.begins, "签名与金额必须在同一个读取事务中")
	require.Zero(t, driver.outsideReads, "名称与账目均不得逃逸到其他快照")
}

func TestConsumptionSnapshotActiveFallbackKeepsFingerprint(t *testing.T) {
	f := newConsumptionFixture(t)
	r, _ := f.addRequest(t, time.Now(), "model-a", request.StatusCompleted, 100, 10, 0, 0, 2, nil)
	input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodToday, OmitPerformance: true}
	ctx := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
	_, err := f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	f.client.UsageLog.Update().Where(usagelog.RequestIDEQ(r.ID)).SetTotalCost(7).SaveX(f.ctx)
	injected := false
	f.client.ConsumptionSnapshot.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if _, saving := m.Field("payload"); saving && !injected {
				injected = true
				f.client.UsageLog.Update().Where(usagelog.RequestIDEQ(r.ID)).SetTotalCost(8).SaveX(f.ctx)
			}
			return next.Mutate(ctx, m)
		})
	})
	require.NoError(t, f.service.fillConsumptionSnapshot(f.ctx))
	f.client.UsageLog.Update().Where(usagelog.RequestIDEQ(r.ID)).SetTotalCost(2).SaveX(f.ctx)
	f.client.ConsumptionSnapshot.Update().SetCheckedAt(time.Now().Add(-2 * time.Minute)).SetLeaseUntil(time.Unix(0, 0)).ExecX(f.ctx)
	page, err := f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.Equal(t, 2.0, page.Slices[0].Entries[0].Metrics.TotalCost)
}
