package ent

import (
	"context"
	"database/sql"
	"fmt"

	"entgo.io/ent/dialect"
)

// mutationDriver keeps the original builder (including SQL modifiers and bulk
// hooks) inside the transaction selected by a mutation hook. The selection is
// context-local; concurrent requests never replace a shared driver's state.
type mutationDriver struct{ dialect.Driver }
type mutationTransactionKey struct{ driver *mutationDriver }
type statisticsTransactionContextKey struct{}
type statisticsTransactionContext struct {
	driver *mutationDriver
	tx     *Tx
}

func statisticsDriver(driver dialect.Driver) dialect.Driver {
	for {
		debug, ok := driver.(*dialect.DebugDriver)
		if !ok {
			return driver
		}
		driver = debug.Driver
	}
}

func statisticsBatchContext(root, current context.Context) context.Context {
	state, ok := current.Value(statisticsTransactionContextKey{}).(statisticsTransactionContext)
	if !ok {
		return root
	}
	root = context.WithValue(root, mutationTransactionKey{state.driver}, state.tx.Client().driver)
	root = context.WithValue(root, statisticsTransactionContextKey{}, state)
	return NewContext(NewTxContext(root, state.tx), state.tx.Client())
}

func (d *mutationDriver) selected(ctx context.Context) dialect.Driver {
	if tx, ok := ctx.Value(mutationTransactionKey{d}).(dialect.Driver); ok {
		return tx
	}
	// RunAsHost intentionally drops caller values but retains its explicit
	// transaction client. Resolve that client without carrying caller authority.
	if client := FromContext(ctx); client != nil {
		if tx, ok := client.driver.(*txDriver); ok && statisticsDriver(tx.drv) == d {
			return tx
		}
	}
	return d.Driver
}

func (d *mutationDriver) Query(ctx context.Context, query string, args, result any) error {
	return d.selected(ctx).Query(ctx, query, args, result)
}

func (d *mutationDriver) Exec(ctx context.Context, query string, args, result any) error {
	return d.selected(ctx).Exec(ctx, query, args, result)
}

func (d *mutationDriver) Tx(ctx context.Context) (dialect.Tx, error) {
	return d.selected(ctx).Tx(ctx)
}

func (d *mutationDriver) BeginTx(ctx context.Context, options *sql.TxOptions) (dialect.Tx, error) {
	return d.selected(ctx).(interface {
		BeginTx(context.Context, *sql.TxOptions) (dialect.Tx, error)
	}).BeginTx(ctx, options)
}

// EnableStatisticsTransactions is called before a new client is exposed.
// Only mutations which affect statistics need the context-aware driver.
func (c *Client) EnableStatisticsTransactions() {
	if _, ok := statisticsDriver(c.driver).(*txDriver); ok {
		return
	}
	if _, ok := c.driver.(*mutationDriver); ok {
		return
	}
	driver := &mutationDriver{c.driver}
	c.driver = driver
	c.Request.driver = driver
	c.RequestExecution.driver = driver
	c.UsageLog.driver = driver
	c.UsageArchive.driver = driver
	c.BudgetCharge.driver = driver
	c.CacheEffectivenessWindow.driver = driver
	c.APIKey.driver = driver
	c.APIKeyGroup.driver = driver
	c.User.driver = driver
	c.Channel.driver = driver
	c.Project.driver = driver
}

// WithStatisticsTransaction commits the source mutation and its statistics
// revision together. Already-transactional callers retain ownership of commit.
func (c *Client) WithStatisticsTransaction(ctx context.Context, run func(context.Context, *Client) (Value, error)) (Value, error) {
	if _, ok := statisticsDriver(c.driver).(*txDriver); ok {
		return run(ctx, c)
	}
	driver, ok := statisticsDriver(c.driver).(*mutationDriver)
	if !ok {
		return nil, fmt.Errorf("statistics transaction driver is not installed")
	}
	if _, ok := ctx.Value(mutationTransactionKey{driver}).(dialect.Driver); ok {
		return run(ctx, FromContext(ctx))
	}
	tx, err := c.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	txCtx := context.WithValue(ctx, mutationTransactionKey{driver}, tx.Client().driver)
	txCtx = context.WithValue(txCtx, statisticsTransactionContextKey{}, statisticsTransactionContext{driver, tx})
	txCtx = NewContext(NewTxContext(txCtx, tx), tx.Client())
	value, err := run(txCtx, tx.Client())
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return value, nil
}
