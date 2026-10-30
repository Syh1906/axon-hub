package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/ent/budgetchange"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/internal/scopes"
)

// BudgetOwner checks project scoping and the existing credential visibility
// rules before any accounting query obtains an internal system context.
func (s *APIKeyService) BudgetOwner(ctx context.Context, kind string, ownerID int, write bool) (int, error) {
	if write {
		if err := authz.RequireScope(ctx, scopes.ScopeWriteAPIKeys); err != nil {
			return 0, err
		}
	}
	projectID, err := s.groupProject(ctx, write)
	if err != nil {
		return 0, err
	}
	db := s.entFromContext(ctx)
	switch kind {
	case "key":
		_, err = db.APIKey.Query().Where(apikey.IDEQ(ownerID), apikey.ProjectIDEQ(projectID)).Only(ctx)
	case "group":
		_, err = db.APIKeyGroup.Query().Where(apikeygroup.IDEQ(ownerID), apikeygroup.ProjectIDEQ(projectID)).Only(ctx)
	default:
		return 0, fmt.Errorf("invalid budget owner kind")
	}
	return projectID, err
}

func (s *APIKeyService) Budget(ctx context.Context, kind string, ownerID int) (*BudgetView, error) {
	projectID, err := s.BudgetOwner(ctx, kind, ownerID, false)
	if err != nil {
		return nil, err
	}
	return NewBudgetService(s.db).View(ctx, projectID, kind, ownerID, s.SystemService.TimeLocation(ctx).String())
}

// View is used only after explicit management or self-usage authorization.
// It computes a display snapshot without creating accounts or changing versions.
func (s *BudgetService) View(ctx context.Context, projectID int, kind string, ownerID int, defaultTimezone string) (view *BudgetView, err error) {
	err = s.transaction(ctx, func(ctx context.Context, db *ent.Client) error {
		account, err := db.BudgetAccount.Query().Where(budgetaccount.ProjectIDEQ(projectID), budgetaccount.OwnerKindEQ(budgetaccount.OwnerKind(kind)), budgetaccount.OwnerIDEQ(ownerID)).Only(ctx)
		if ent.IsNotFound(err) {
			state := budget.NewState()
			state.Policy.Rules = initialBudgetRules()
			account = &ent.BudgetAccount{ProjectID: projectID, OwnerKind: budgetaccount.OwnerKind(kind), OwnerID: ownerID, State: state}
		} else if err != nil {
			return err
		}
		if account.Revision == 0 {
			account.State.Policy.Timezone = defaultTimezone
		}
		view, err = buildBudgetView(ctx, db, account, time.Now().UTC())
		return err
	})
	return
}

func cloneBudgetState(state budget.State) (budget.State, error) {
	data, err := json.Marshal(state)
	if err != nil {
		return budget.State{}, err
	}
	var result budget.State
	err = json.Unmarshal(data, &result)
	return result, err
}

func validateBudgetRules(input BudgetSettingsInput) ([]budget.Rule, error) {
	rules := make([]budget.Rule, 0, len(input.Rules))
	for _, r := range input.Rules {
		if r == nil {
			return nil, fmt.Errorf("missing budget rule")
		}
		amount, err := budget.ParseAmount(r.Limit)
		if err != nil {
			return nil, err
		}
		rules = append(rules, budget.Rule{Period: r.Period, Mode: r.Mode, Limit: amount, Minute: r.Minute, Timezone: input.Timezone})
	}
	if len(rules) != len(budget.Periods) {
		return nil, fmt.Errorf("all five budget rules are required")
	}
	if err := (budget.Policy{Rules: rules, Timezone: input.Timezone}).Validate(); err != nil {
		return nil, err
	}
	if input.OperationID == "" || len(input.OperationID) > 64 {
		return nil, fmt.Errorf("invalid budget operation identifier")
	}
	return rules, nil
}

func existingBudgetChange(ctx context.Context, db *ent.Client, account *ent.BudgetAccount, id string, operation budgetchange.Operation) (bool, error) {
	change, err := db.BudgetChange.Query().Where(budgetchange.ProjectIDEQ(account.ProjectID), budgetchange.OperationIDEQ(id)).Only(ctx)
	if ent.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if change.AccountID != account.ID || change.Operation != operation {
		return false, fmt.Errorf("budget operation identifier was already used")
	}
	return true, nil
}

func (s *APIKeyService) SaveBudget(ctx context.Context, kind string, ownerID int, input BudgetSettingsInput) (view *BudgetView, err error) {
	projectID, err := s.BudgetOwner(ctx, kind, ownerID, true)
	if err != nil {
		return nil, err
	}
	actor, _ := contexts.GetUser(ctx)
	rules, err := validateBudgetRules(input)
	if err != nil {
		return nil, err
	}
	service := NewBudgetService(s.db)
	err = service.transaction(ctx, func(ctx context.Context, db *ent.Client) error {
		account, err := LockBudgetAccount(ctx, db, projectID, budgetaccount.OwnerKind(kind), ownerID)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		replayed, err := existingBudgetChange(ctx, db, account, input.OperationID, budgetchange.OperationConfigure)
		if err != nil {
			return err
		}
		if replayed {
			view, err = buildBudgetView(ctx, db, account, now)
			return err
		}
		if account.Revision != int64(input.Revision) {
			return fmt.Errorf("budget settings changed; reload before saving")
		}
		if err = advanceBudget(account, now); err != nil {
			return err
		}
		before, err := cloneBudgetState(account.State)
		if err != nil {
			return err
		}
		state := &account.State
		if len(state.Policy.Rules) == 0 {
			state.Policy.Rules = initialBudgetRules()
		}
		wasStarted := !state.Policy.StartedAt.IsZero()
		restore := map[budget.Period]bool{}
		for _, rule := range input.Rules {
			restore[rule.Period] = rule.RestoreCalendar
		}
		for _, desired := range rules {
			for i, current := range state.Policy.Rules {
				if current.Period != desired.Period {
					continue
				}
				oldZone := current.Timezone
				if oldZone == "" {
					oldZone = state.Policy.Timezone
					current.Timezone = oldZone
				}
				scheduleChanged := current.Mode != desired.Mode || current.Minute != desired.Minute || oldZone != desired.Timezone || (restore[desired.Period] && state.Policy.Clocks[desired.Period].Anchor != nil)
				if scheduleChanged && wasStarted && desired.Period != budget.Total {
					loc, err := budgetRuleLocation(current, *state)
					if err != nil {
						return err
					}
					window, err := current.Bounds(now, state.Policy.Clocks[current.Period], loc)
					if err != nil {
						return err
					}
					at := now
					if current.Mode == budget.Rolling {
						at = now.Add(current.Duration())
					} else if window.End != nil {
						at = *window.End
					}
					if at.After(now) {
						if previous, ok := state.Pending[current.Period]; ok && previous.Rule.Mode == desired.Mode && previous.Rule.Minute == desired.Minute && previous.Rule.Timezone == desired.Timezone {
							at = previous.At
						}
						state.Pending[current.Period] = budget.PendingRule{Rule: desired, At: at}
						current.Limit = desired.Limit
						state.Policy.Rules[i] = current
						continue
					}
				}
				state.Policy.Rules[i] = desired
				delete(state.Pending, desired.Period)
			}
		}
		state.Policy.Timezone = input.Timezone
		state.Policy.ExpiresAt = input.ExpiresAt
		if !wasStarted && state.HasLimits() {
			state.Policy.StartedAt = now
		}
		account.Revision++
		if err = db.BudgetAccount.UpdateOneID(account.ID).SetState(*state).SetRevision(account.Revision).Exec(ctx); err != nil {
			return err
		}
		if err = db.BudgetChange.Create().SetProjectID(projectID).SetAccountID(account.ID).SetActorID(actor.ID).SetOperationID(input.OperationID).SetOperation(budgetchange.OperationConfigure).SetBefore(before).SetAfter(*state).Exec(ctx); err != nil {
			return err
		}
		view, err = buildBudgetView(ctx, db, account, now)
		return err
	})
	return
}

type BudgetResetInput struct {
	Periods     []budget.Period `json:"periods"`
	ResetCycle  bool            `json:"resetCycle"`
	Revision    int             `json:"revision"`
	OperationID string          `json:"operationId"`
}

func (s *APIKeyService) ResetBudget(ctx context.Context, kind string, ownerID int, input BudgetResetInput) (view *BudgetView, err error) {
	projectID, err := s.BudgetOwner(ctx, kind, ownerID, true)
	if err != nil {
		return nil, err
	}
	actor, _ := contexts.GetUser(ctx)
	if len(input.Periods) == 0 || input.OperationID == "" || len(input.OperationID) > 64 {
		return nil, fmt.Errorf("select budget periods and an operation identifier")
	}
	defaultTimezone := s.SystemService.TimeLocation(ctx).String()
	service := NewBudgetService(s.db)
	err = service.transaction(ctx, func(ctx context.Context, db *ent.Client) error {
		account, err := LockBudgetAccount(ctx, db, projectID, budgetaccount.OwnerKind(kind), ownerID)
		if err != nil {
			return err
		}
		if account.Revision == 0 {
			account.State.Policy.Timezone = defaultTimezone
		}
		now := time.Now().UTC()
		replayed, err := existingBudgetChange(ctx, db, account, input.OperationID, budgetchange.OperationReset)
		if err != nil {
			return err
		}
		if replayed {
			view, err = buildBudgetView(ctx, db, account, now)
			return err
		}
		if account.Revision != int64(input.Revision) {
			return fmt.Errorf("budget settings changed; reload the reset preview")
		}
		if err = advanceBudget(account, now); err != nil {
			return err
		}
		before, err := cloneBudgetState(account.State)
		if err != nil {
			return err
		}
		state := &account.State
		if err := applyBudgetReset(account, input, now); err != nil {
			return err
		}
		account.Revision++
		if err = db.BudgetAccount.UpdateOneID(account.ID).SetState(*state).SetRevision(account.Revision).Exec(ctx); err != nil {
			return err
		}
		if err = db.BudgetChange.Create().SetProjectID(projectID).SetAccountID(account.ID).SetActorID(actor.ID).SetOperationID(input.OperationID).SetOperation(budgetchange.OperationReset).SetBefore(before).SetAfter(*state).Exec(ctx); err != nil {
			return err
		}
		view, err = buildBudgetView(ctx, db, account, now)
		return err
	})
	return
}

func applyBudgetReset(account *ent.BudgetAccount, input BudgetResetInput, now time.Time) error {
	state := &account.State
	selected := map[budget.Period]bool{}
	for _, period := range input.Periods {
		if selected[period] {
			return fmt.Errorf("duplicate reset period")
		}
		selected[period] = true
		found := false
		for _, rule := range state.Policy.Rules {
			if rule.Period != period {
				continue
			}
			found = true
			loc, err := budgetRuleLocation(rule, *state)
			if err != nil {
				return err
			}
			restart := input.ResetCycle && rule.CanRestartCycle()
			clock, err := rule.Reset(now, state.Policy.Clocks[period], loc, restart)
			if err != nil {
				return err
			}
			state.Policy.Clocks[period] = clock
			window, err := rule.Bounds(now, clock, loc)
			if err != nil {
				return err
			}
			state.Counters[period] = budget.Counter{Start: window.Start, ResetVersion: clock.ResetVersion}
			if pending, ok := state.Pending[period]; ok && restart && window.End != nil {
				pending.At = *window.End
				state.Pending[period] = pending
			}
		}
		if !found {
			return fmt.Errorf("unknown reset period")
		}
	}

	return nil
}

func (s *APIKeyService) PreviewBudgetReset(ctx context.Context, kind string, ownerID int, input BudgetResetInput) (view *BudgetView, err error) {
	projectID, err := s.BudgetOwner(ctx, kind, ownerID, true)
	if err != nil {
		return nil, err
	}
	defaultTimezone := s.SystemService.TimeLocation(ctx).String()
	err = NewBudgetService(s.db).transaction(ctx, func(ctx context.Context, db *ent.Client) error {
		account, err := db.BudgetAccount.Query().Where(budgetaccount.ProjectIDEQ(projectID), budgetaccount.OwnerKindEQ(budgetaccount.OwnerKind(kind)), budgetaccount.OwnerIDEQ(ownerID)).Only(ctx)
		if err != nil {
			return err
		}
		if account.Revision != int64(input.Revision) {
			return fmt.Errorf("budget settings changed; reload the reset preview")
		}
		if account.Revision == 0 {
			account.State.Policy.Timezone = defaultTimezone
		}
		now := time.Now().UTC()
		if err = advanceBudget(account, now); err != nil {
			return err
		}
		if err = applyBudgetReset(account, input, now); err != nil {
			return err
		}
		view, err = buildBudgetView(ctx, db, account, now)
		return err
	})
	return
}
