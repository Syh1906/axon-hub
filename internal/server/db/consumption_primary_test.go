package db

import (
	"context"
	"testing"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
)

type consumptionReadDriver struct{ reads int }

func (d *consumptionReadDriver) Query(context.Context, string, any, any) error { d.reads++; return nil }
func (*consumptionReadDriver) Exec(context.Context, string, any, any) error    { return nil }
func (*consumptionReadDriver) Tx(context.Context) (dialect.Tx, error)          { return nil, nil }
func (*consumptionReadDriver) Close() error                                    { return nil }
func (*consumptionReadDriver) Dialect() string                                 { return dialect.Postgres }

func TestConsumptionInvalidationReadsPrimary(t *testing.T) {
	primary, replica := &consumptionReadDriver{}, &consumptionReadDriver{}
	driver := newRouterDriver(primary, replica)
	ctx := ent.NewQueryContext(t.Context(), &ent.QueryContext{Type: "Request"})
	require.NoError(t, driver.Query(ctx, "SELECT id FROM requests", nil, nil))
	require.Equal(t, 1, replica.reads)
	require.NoError(t, driver.Query(contexts.WithPrimaryRead(ctx), "SELECT id FROM requests", nil, nil))
	require.Equal(t, 1, primary.reads)
	require.Equal(t, 1, replica.reads)
}
