package biz

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/pkg/budget"
)

// Settle commits a single actual cost, both constraint postings, counters and
// the usage log together. A nil cost retains the reservation for reconciliation.
// handled=false means this attempt predates budget activation.
func (s *BudgetService) Settle(ctx context.Context, executionID int, cost *budget.Amount, createLog func(context.Context, *ent.Client) (*ent.UsageLog, error)) (result *ent.UsageLog, handled bool, err error) {
	err = s.withCharge(ctx, executionID, func(ctx context.Context, db *ent.Client, charge *ent.BudgetCharge, accounts []*ent.BudgetAccount) error {
		handled = true
		if charge.UsageLogID != nil {
			var err error
			result, err = db.UsageLog.Get(ctx, *charge.UsageLogID)
			if err != nil && !ent.IsNotFound(err) {
				return err
			}
		}
		if charge.Status == budgetcharge.StatusSettled || charge.Status == budgetcharge.StatusReleased {
			return nil
		}
		if charge.UsageLogID == nil && createLog != nil {
			var err error
			result, err = createLog(ctx, db)
			if err != nil {
				return err
			}
		}
		update := db.BudgetCharge.UpdateOneID(charge.ID)
		if result != nil {
			update.SetUsageLogID(result.ID)
		}
		if cost == nil {
			return update.SetStatus(budgetcharge.StatusUnknown).SetCheckAfter(time.Now().UTC().Add(time.Minute)).Exec(ctx)
		}
		if *cost < 0 {
			return fmt.Errorf("negative budget settlement")
		}
		now := time.Now().UTC()
		for _, account := range accounts {
			versions := charge.KeyVersions
			if account.ID != charge.KeyAccountID {
				versions = charge.GroupVersions
			}
			if versions[budget.Total] < 0 {
				continue
			}
			if account.Reserved < charge.Estimate {
				return fmt.Errorf("inconsistent budget reservation")
			}
			if err := advanceBudget(account, now); err != nil {
				return err
			}
			// Rebuild before inserting the new posting, so every counter adds the
			// settlement exactly once even when several counters are absent.
			if _, err := budgetUsage(ctx, db, account, now); err != nil {
				return err
			}
			state := &account.State
			if versions[budget.Total] >= 0 && !state.Policy.StartedAt.IsZero() {
				if err := db.BudgetPosting.Create().SetProjectID(account.ProjectID).SetAccountID(account.ID).SetChargeID(charge.ID).
					SetAmount(int64(*cost)).SetSettledAt(now).SetFiveHourVersion(versions[budget.FiveHour]).SetDailyVersion(versions[budget.Daily]).
					SetWeeklyVersion(versions[budget.Weekly]).SetMonthlyVersion(versions[budget.Monthly]).SetTotalVersion(versions[budget.Total]).Exec(ctx); err != nil {
					return err
				}
				for _, rule := range state.Policy.Rules {
					loc, err := budgetRuleLocation(rule, *state)
					if err != nil {
						return err
					}
					clock := state.Policy.Clocks[rule.Period]
					if versions[rule.Period] != clock.ResetVersion {
						continue
					}
					clock = rule.StartFixedFiveHour(now, clock, *cost)
					state.Policy.Clocks[rule.Period] = clock
					if rule.Mode == budget.Rolling {
						continue
					}
					window, err := rule.Bounds(now, clock, loc)
					if err != nil {
						return err
					}
					counter := state.Counters[rule.Period]
					if !sameBudgetTime(counter.Start, window.Start) || counter.ResetVersion != clock.ResetVersion {
						counter = budget.Counter{Start: window.Start, ResetVersion: clock.ResetVersion}
					}
					if int64(counter.Amount) > math.MaxInt64-int64(*cost) {
						return fmt.Errorf("budget settlement overflow")
					}
					counter.Amount += *cost
					state.Counters[rule.Period] = counter
				}
			}
			if err := db.BudgetAccount.UpdateOneID(account.ID).SetState(*state).AddReserved(-charge.Estimate).Exec(ctx); err != nil {
				return err
			}
		}
		return update.SetActual(int64(*cost)).SetStatus(budgetcharge.StatusSettled).SetSettledAt(now).Exec(ctx)
	})
	return
}
