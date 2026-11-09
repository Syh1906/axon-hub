package biz

import (
	"context"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
)

var errCaptureConsumptionQuery = errors.New("capture consumption query")

type consumptionQueryCapture struct {
	query string
	args  []any
}

func (d *consumptionQueryCapture) Query(_ context.Context, query string, args, _ any) error {
	d.query = query
	d.args, _ = args.([]any)
	return errCaptureConsumptionQuery
}
func (*consumptionQueryCapture) Exec(context.Context, string, any, any) error {
	return errCaptureConsumptionQuery
}
func (*consumptionQueryCapture) Tx(context.Context) (dialect.Tx, error) {
	return nil, errCaptureConsumptionQuery
}
func (*consumptionQueryCapture) Close() error    { return nil }
func (*consumptionQueryCapture) Dialect() string { return dialect.Postgres }

// Exercise the production builders, including the raw SQL expressions inside
// JOIN predicates. SQLite accepts MySQL backticks and cannot catch this outage.
func TestConsumptionPostgresQueryIdentifiers(t *testing.T) {
	ctx := authz.WithTestBypass(t.Context())
	end := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	start := end.Add(-24 * time.Hour)
	for _, dimension := range []ConsumptionDimension{
		ConsumptionDimensionChannel, ConsumptionDimensionAPIKeyGroup, ConsumptionDimensionModel,
		ConsumptionDimensionAPIKey, ConsumptionDimensionUser,
	} {
		for _, scope := range []int{0, 1} {
			for _, variant := range []string{"wide", "search", "group", "all-time"} {
				t.Run(string(dimension)+"/"+variant+lo.Ternary(scope == 0, "/admin", "/project"), func(t *testing.T) {
					driver := &consumptionQueryCapture{}
					service := &ConsumptionService{client: ent.NewClient(ent.Driver(driver))}
					filter := ConsumptionFilter{Dimension: dimension, Metric: ConsumptionMetricCost}
					from := &start
					switch variant {
					case "search":
						filter.Search = lo.ToPtr("fixture")
					case "group":
						filter.GroupRef = lo.ToPtr(1)
					case "all-time":
						from = nil
					}
					_, err := service.queryGroupsSnapshot(ctx, scope, filter, from, end)
					require.ErrorIs(t, err, errCaptureConsumptionQuery)
					require.NotEmpty(t, driver.query)
					require.NotContains(t, driver.query, "`", "raw fragments must use the same dialect as the surrounding query")
				})
			}
		}
	}
	for _, operation := range []string{"attempts", "execution archive"} {
		t.Run(operation, func(t *testing.T) {
			driver := &consumptionQueryCapture{}
			client := ent.NewClient(ent.Driver(driver))
			if operation == "attempts" {
				_, err := queryChannelAttemptGroups(ctx, client, 1, ConsumptionFilter{}, nil, &start, end)
				require.ErrorIs(t, err, errCaptureConsumptionQuery)
			} else {
				require.ErrorIs(t, archiveExecutionStatistics(ctx, client, []int{1}), errCaptureConsumptionQuery)
			}
			require.NotContains(t, driver.query, "`")
		})
	}
}
