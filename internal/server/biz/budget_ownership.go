package biz

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/log"
)

const (
	budgetOwnerTTL           = 2 * time.Minute
	budgetOwnerRenewInterval = 30 * time.Second
	budgetOwnerRenewTimeout  = 10 * time.Second
)

// BudgetAttempt owns only this attempt's transport and renewal lifetime. It is
// handed to the stream on return and stopped before retrying another execution.
type BudgetAttempt struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	done   chan struct{}
}

func (s *BudgetService) KeepAttemptAlive(ctx context.Context, executionID int) *BudgetAttempt {
	return s.keepAttemptAlive(ctx, executionID, budgetOwnerRenewInterval)
}

func (s *BudgetService) keepAttemptAlive(ctx context.Context, executionID int, interval time.Duration) *BudgetAttempt {
	ctx, cancel := context.WithCancelCause(ctx)
	a := &BudgetAttempt{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(a.done)
		defer func() {
			if value := recover(); value != nil {
				log.Error(ctx, "budget owner renewal panic", log.Any("panic", value))
				cancel(&BudgetDenied{Reason: "accounting_unavailable"})
			}
		}()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				renewCtx, stop := context.WithTimeout(ctx, budgetOwnerRenewTimeout)
				active, err := s.renewAttemptOwner(renewCtx, executionID)
				stop()
				if err != nil {
					if ctx.Err() == nil {
						log.Error(ctx, "budget execution owner lost", log.Int("execution_id", executionID), log.Cause(err))
						cancel(&BudgetDenied{Reason: "accounting_unavailable"})
					}
					return
				}
				if !active {
					return
				}
			}
		}
	}()
	return a
}

// Bind preserves the executor's own timeout as well as owner-loss cancellation.
func (a *BudgetAttempt) Bind(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(a.ctx, func() { cancel(context.Cause(a.ctx)) })
	if err := context.Cause(a.ctx); err != nil {
		cancel(err)
	}
	return ctx, func() { stop(); cancel(context.Canceled) }
}

func (a *BudgetAttempt) Err() error {
	if err := context.Cause(a.ctx); IsBudgetDenied(err) {
		return err
	}
	return nil
}

func (a *BudgetAttempt) Stop() {
	a.cancel(context.Canceled)
	<-a.done
}

func (s *BudgetService) checkAttemptOwner(ctx context.Context, db *ent.Client, charge *ent.BudgetCharge) error {
	now, err := budgetDatabaseTime(ctx, db)
	if err != nil {
		return err
	}
	return s.validateAttemptOwner(charge, now)
}

func (s *BudgetService) validateAttemptOwner(charge *ent.BudgetCharge, now time.Time) error {
	if charge.OwnerToken == nil || *charge.OwnerToken != s.ownerToken || charge.OwnerExpiresAt == nil || !charge.OwnerExpiresAt.After(now) {
		return fmt.Errorf("budget execution owner expired or revoked")
	}
	return nil
}

func (s *BudgetService) renewAttemptOwner(ctx context.Context, executionID int) (active bool, err error) {
	err = s.withCharge(ctx, executionID, func(ctx context.Context, db *ent.Client, charge *ent.BudgetCharge, _ []*ent.BudgetAccount) error {
		// Empty is a permanent revocation marker; NULL is legacy attribution.
		if charge.OwnerToken != nil && *charge.OwnerToken == "" {
			return fmt.Errorf("budget execution owner revoked")
		}
		// Normal finalization/async handoff is not owner loss. RecordUsage's
		// unknown status, on its own, does not end a live attempt.
		if charge.Status == budgetcharge.StatusSettled || charge.Status == budgetcharge.StatusReleased || charge.ReservationReleasedAt != nil || charge.AsyncTask != nil {
			return nil
		}
		now, err := budgetDatabaseTime(ctx, db)
		if err != nil {
			return err
		}
		if err := s.validateAttemptOwner(charge, now); err != nil {
			return err
		}
		active = true
		return db.BudgetCharge.UpdateOneID(charge.ID).SetOwnerExpiresAt(now.Add(budgetOwnerTTL)).SetCheckAfter(time.Now().UTC().Add(time.Minute)).Exec(ctx)
	})
	return
}

// EndOwnedAttempt fences retry using the durable owner state, even when the
// transport returns before the renewal goroutine observes a revocation.
// Late usage settlement remains independent of this execution authority.
func (s *BudgetService) EndOwnedAttempt(ctx context.Context, executionID int) (lost bool, err error) {
	err = s.withCharge(ctx, executionID, func(ctx context.Context, db *ent.Client, charge *ent.BudgetCharge, accounts []*ent.BudgetAccount) error {
		if charge.OwnerToken == nil || *charge.OwnerToken != s.ownerToken {
			lost = true
			// This caller cannot end another live owner's or a legacy attempt.
			return nil
		}
		finished := charge.Status == budgetcharge.StatusSettled || charge.Status == budgetcharge.StatusReleased || charge.ReservationReleasedAt != nil || charge.AsyncTask != nil
		if !finished {
			now, err := budgetDatabaseTime(ctx, db)
			if err != nil {
				return err
			}
			lost = s.validateAttemptOwner(charge, now) != nil
			if lost {
				if err := db.BudgetCharge.UpdateOneID(charge.ID).SetOwnerToken("").Exec(ctx); err != nil {
					return err
				}
			}
		}
		return endBudgetAttempt(ctx, db, charge, accounts)
	})
	return
}

// Lease timestamps use the primary database clock, not the wall clocks of
// different gateway instances. Call only inside a primary transaction.
func budgetDatabaseTime(ctx context.Context, db *ent.Client) (time.Time, error) {
	var query string
	switch db.Driver().Dialect() {
	case dialect.SQLite:
		query = "SELECT CAST(strftime('%s', 'now') AS INTEGER)"
	case dialect.Postgres:
		query = "SELECT FLOOR(EXTRACT(EPOCH FROM clock_timestamp()))::bigint"
	case dialect.MySQL:
		query = "SELECT UNIX_TIMESTAMP()"
	default:
		return time.Time{}, fmt.Errorf("unsupported budget database dialect %q", db.Driver().Dialect())
	}
	var rows sql.Rows
	if err := db.Driver().Query(ctx, query, []any{}, &rows); err != nil {
		return time.Time{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return time.Time{}, err
		}
		return time.Time{}, fmt.Errorf("missing budget database time")
	}
	var seconds int64
	if err := rows.Scan(&seconds); err != nil {
		return time.Time{}, err
	}
	return time.Unix(seconds, 0).UTC(), rows.Err()
}
