package biz

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
)

// The database must be a disposable local test instance. Each case owns a schema.
func newStatisticsPostgresFixture(t *testing.T) *consumptionFixture {
	t.Helper()
	dsn := os.Getenv("AXONHUB_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未配置 AXONHUB_TEST_PG_DSN")
	}
	config, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	admin := stdlib.OpenDB(*config.Copy())
	t.Cleanup(func() { _ = admin.Close() })
	schema := "statistics_" + uuid.NewString()[:8]
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf("CREATE SCHEMA %q", schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.ExecContext(context.Background(), fmt.Sprintf("DROP SCHEMA %q CASCADE", schema))
		require.NoError(t, err)
	})
	config.RuntimeParams["search_path"] = schema
	pool := stdlib.OpenDB(*config)
	pool.SetMaxOpenConns(4)
	client := enttest.NewClient(t, enttest.WithOptions(ent.Driver(sql.OpenDB(dialect.Postgres, pool))))
	t.Cleanup(func() { _ = client.Close() })
	return newConsumptionFixtureWithClient(t, client)
}

func newStatisticsMySQLFixture(t *testing.T) *consumptionFixture {
	t.Helper()
	dsn := os.Getenv("AXONHUB_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未配置 AXONHUB_TEST_MYSQL_DSN")
	}
	config, err := mysql.ParseDSN(dsn)
	require.NoError(t, err)
	admin, err := stdsql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })
	schema := "statistics_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	_, err = admin.ExecContext(t.Context(), "CREATE DATABASE `"+schema+"`")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.ExecContext(context.Background(), "DROP DATABASE `"+schema+"`")
		require.NoError(t, err)
	})
	config.DBName = schema
	pool, err := stdsql.Open("mysql", config.FormatDSN())
	require.NoError(t, err)
	pool.SetMaxOpenConns(4)
	client := enttest.NewClient(t, enttest.WithOptions(ent.Driver(sql.OpenDB(dialect.MySQL, pool))))
	t.Cleanup(func() { _ = client.Close() })
	return newConsumptionFixtureWithClient(t, client)
}

// Pause a real projection after reading its inputs but before publishing them.
// Another connection can commit a correction or prepare the same rows meanwhile.
type statisticsPausedDriver struct {
	dialect.Driver
	beforeExec func(context.Context, string) error
}

func (d *statisticsPausedDriver) BeginTx(ctx context.Context, opts *stdsql.TxOptions) (dialect.Tx, error) {
	tx, err := d.Driver.(interface {
		BeginTx(context.Context, *stdsql.TxOptions) (dialect.Tx, error)
	}).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &statisticsPausedTx{Tx: tx, beforeExec: d.beforeExec}, nil
}

type statisticsPausedTx struct {
	dialect.Tx
	beforeExec func(context.Context, string) error
}

func (d *statisticsPausedTx) Exec(ctx context.Context, query string, args, result any) error {
	if err := d.beforeExec(ctx, query); err != nil {
		return err
	}
	return d.Tx.Exec(ctx, query, args, result)
}

func TestStatisticsConcurrentPreparationPostgres(t *testing.T) {
	testStatisticsConcurrentPreparation(t, newStatisticsPostgresFixture)
}

func TestStatisticsConcurrentPreparationMySQL(t *testing.T) {
	testStatisticsConcurrentPreparation(t, newStatisticsMySQLFixture)
}

func testStatisticsConcurrentPreparation(t *testing.T, fixture func(*testing.T) *consumptionFixture) {
	for _, scenario := range []string{"bill correction", "concurrent reader", "standard cost correction", "full consumption reader"} {
		t.Run(scenario, func(t *testing.T) {
			f := fixture(t)
			ctx, cancel := context.WithTimeout(f.ctx, 15*time.Second)
			defer cancel()
			at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
			_, execution := f.addRequest(t, at, "model-a", request.StatusCompleted, 100, 100, 0, 0, 2, lo.ToPtr(int64(200)))
			if scenario == "standard cost correction" {
				f.client.UsageLog.Update().SetCostItems([]objects.CostItem{{Subtotal: decimal.NewFromInt(2),
					PricingEvidence: &objects.CostPricingEvidence{ModeSubtotal: lo.ToPtr(decimal.NewFromInt(3))}}}).SaveX(ctx)
			}
			start, end := at.Truncate(24*time.Hour), at.Truncate(24*time.Hour).Add(24*time.Hour)
			reached, resume := make(chan struct{}), make(chan struct{})
			var once sync.Once
			column := "statistics_ready"
			if scenario == "standard cost correction" {
				column = "statistics_standard_ready"
			}
			driver := &statisticsPausedDriver{Driver: f.client.Driver(), beforeExec: func(ctx context.Context, query string) error {
				if strings.HasPrefix(query, "UPDATE ") && strings.Contains(query, column) {
					once.Do(func() {
						close(reached)
						select {
						case <-resume:
						case <-ctx.Done():
						}
					})
				}
				return ctx.Err()
			}}
			reader := ent.NewClient(ent.Driver(driver))
			read := func(client *ent.Client) error {
				if scenario == "full consumption reader" {
					service := *f.service
					service.client = client
					_, err := service.queryGroupsFacts(ctx, f.project.ID, ConsumptionFilter{Dimension: ConsumptionDimensionModel, Metric: ConsumptionMetricCost}, &start, end)
					return err
				}
				if scenario == "standard cost correction" {
					_, err := queryChannelTodayUsageModels(ctx, client, f.channel.ID, start, end)
					return err
				}
				_, err := queryChannelAttemptGroups(ctx, client, f.project.ID, ConsumptionFilter{}, []int{f.channel.ID}, &start, end)
				return err
			}
			done := make(chan error, 1)
			go func() {
				defer func() {
					if value := recover(); value != nil {
						done <- fmt.Errorf("statistics reader panic: %v", value)
					}
				}()
				done <- read(reader)
			}()
			select {
			case <-reached:
			case err := <-done:
				t.Fatalf("projection did not reach the concurrent write: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			wantSpeed := 100.0
			if scenario == "concurrent reader" {
				require.NoError(t, read(f.client))
			} else {
				bill := f.client.UsageLog.Query().OnlyX(ctx)
				update := bill.Update().SetCompletionTokens(200).SetTotalTokens(300).SetTotalCost(4)
				if scenario == "standard cost correction" {
					update.SetCostItems([]objects.CostItem{{Subtotal: decimal.NewFromInt(4),
						PricingEvidence: &objects.CostPricingEvidence{ModeSubtotal: lo.ToPtr(decimal.NewFromInt(6))}}})
				}
				_, err := update.Save(ctx)
				require.NoError(t, err)
				wantSpeed = 200
			}
			close(resume)
			require.NoError(t, <-done, "a competing preparation must finish with a new consistent snapshot")
			require.NoError(t, read(f.client))
			if scenario == "standard cost correction" {
				bill := f.client.UsageLog.Query().OnlyX(ctx)
				require.True(t, bill.StatisticsStandardReady)
				require.NotNil(t, bill.StatisticsStandardCost)
				require.InDelta(t, 6, *bill.StatisticsStandardCost, 1e-9)
				models, err := queryChannelTodayUsageModels(ctx, f.client, f.channel.ID, start, end)
				require.NoError(t, err)
				require.Len(t, models, 1)
				require.NotNil(t, models[0].StandardCost)
				require.InDelta(t, 6, *models[0].StandardCost, 1e-9)
				require.NotNil(t, models[0].AvgTokensPerSecond)
				require.InDelta(t, wantSpeed, *models[0].AvgTokensPerSecond, 1e-9)
				require.False(t, f.client.RequestExecution.GetX(ctx, execution.ID).StatisticsReady, "hourly reader computes execution metrics without rewriting the source")
			} else {
				current := f.client.RequestExecution.GetX(ctx, execution.ID)
				require.True(t, current.StatisticsReady)
				require.NotNil(t, current.StatisticsOutputSpeed)
				require.InDelta(t, wantSpeed, *current.StatisticsOutputSpeed, 1e-9, "a correction must not leave a stale projection permanently ready")
			}
		})
	}
}
