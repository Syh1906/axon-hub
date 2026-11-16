package biz

import (
	"context"
	stdsql "database/sql"
	"errors"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/go-sql-driver/mysql"

	"github.com/looplj/axonhub/internal/ent"
)

var errStatisticsSourceChanged = errors.New("statistics source changed during preparation")

// A conditional update protects MySQL as well as PostgreSQL: a repeatable read
// must never publish values computed before a concurrently committed correction.
func saveStatisticsProjection(ctx context.Context, client *ent.Client, update *sql.UpdateBuilder, expected int) error {
	query, args := update.Query()
	var result stdsql.Result
	if err := client.Driver().Exec(ctx, query, args, &result); err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != int64(expected) {
		return errStatisticsSourceChanged
	}
	return nil
}

func statisticsSnapshotConflict(err error) bool {
	if errors.Is(err, errStatisticsSourceChanged) {
		return true
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		return state.SQLState() == "40001" || state.SQLState() == "40P01"
	}
	var mysqlError *mysql.MySQLError
	return errors.As(err, &mysqlError) && mysqlError.Number == 1213
}

// Only derived statistics are written here. On a write conflict, replay the
// entire snapshot (including source revisions), never just its final UPDATE.
// Existing transactions retain commit/retry ownership with their caller.
func readStatisticsSnapshot[T any](ctx context.Context, client *ent.Client, readOnly bool, read func(*ent.Client) (T, error)) (T, error) {
	if _, transactional := client.Driver().(dialect.Tx); transactional {
		return read(client)
	}
	opts := &stdsql.TxOptions{ReadOnly: readOnly, Isolation: stdsql.LevelRepeatableRead}
	if client.Driver().Dialect() == dialect.SQLite {
		opts = &stdsql.TxOptions{}
	}
	for attempt := 0; ; attempt++ {
		var zero T
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		tx, err := client.BeginTx(ctx, opts)
		if err != nil {
			return zero, err
		}
		value, err := func() (T, error) {
			defer tx.Rollback()
			value, err := read(tx.Client())
			if err == nil {
				err = tx.Commit()
			}
			return value, err
		}()
		if err == nil {
			return value, nil
		}
		if attempt >= 2 || !statisticsSnapshotConflict(err) {
			return zero, err
		}
	}
}
