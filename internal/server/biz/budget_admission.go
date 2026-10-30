package biz

import (
	"context"
	"errors"
	"fmt"
	"github.com/looplj/axonhub/llm/httpclient"
	"math"
	"net/http"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/pkg/budget"
)

// BudgetDenied distinguishes a global restriction from a candidate quote that
// is too large. Only the latter may try another already authorized candidate.
type BudgetDenied struct {
	Scope         string
	Period        budget.Period
	Reason        string
	CandidateOnly bool
}

func (e *BudgetDenied) Error() string {
	return fmt.Sprintf("%s budget: %s (%s)", e.Scope, e.Reason, e.Period)
}
func (e *BudgetDenied) Unwrap() error {
	status := http.StatusTooManyRequests
	if e.Reason == "expired" {
		status = http.StatusForbidden
		if e.Scope == "key" {
			status = http.StatusUnauthorized
		}
	}
	if e.Reason == "accounting_unavailable" || e.Reason == "pricing_unavailable" {
		status = http.StatusServiceUnavailable
	}
	return &httpclient.Error{StatusCode: status, Status: http.StatusText(status), Body: fmt.Appendf(nil, `{"error":{"type":"budget_error","code":%q,"message":%q}}`, e.Reason, e.Error())}
}
func IsBudgetDenied(err error) bool { var e *BudgetDenied; return errors.As(err, &e) }

func (s *BudgetService) Reserve(ctx context.Context, projectID, keyID, requestID, executionID int, estimate budget.Amount, quoteErr error) error {
	return s.transaction(ctx, func(ctx context.Context, db *ent.Client) error {
		keyAccount, err := LockBudgetAccount(ctx, db, projectID, budgetaccount.OwnerKindKey, keyID)
		if err != nil {
			return err
		}
		key, err := db.APIKey.Query().Where(apikey.IDEQ(keyID), apikey.ProjectIDEQ(projectID), apikey.StatusEQ(apikey.StatusEnabled)).Only(ctx)
		if err != nil {
			return err
		}
		existing, err := db.BudgetCharge.Query().Where(budgetcharge.ExecutionIDEQ(executionID)).Only(ctx)
		if err == nil {
			if existing.KeyID != keyID || existing.ProjectID != projectID || existing.RequestID != requestID {
				return fmt.Errorf("budget execution identity mismatch")
			}
			if existing.Status != budgetcharge.StatusPrepared {
				return fmt.Errorf("budget execution already dispatched")
			}
			return nil
		}
		if !ent.IsNotFound(err) {
			return err
		}
		groupID := key.GroupRef
		// Retries retain the first dispatched attempt's historical group.
		first, err := db.BudgetCharge.Query().Where(budgetcharge.ProjectIDEQ(projectID), budgetcharge.RequestIDEQ(requestID), budgetcharge.StatusIn(budgetcharge.StatusSent, budgetcharge.StatusUnknown, budgetcharge.StatusSettled)).Order(ent.Asc(budgetcharge.FieldID)).First(ctx)
		if err == nil {
			groupID = first.GroupID
		} else if !ent.IsNotFound(err) {
			return err
		}
		var groupAccount *ent.BudgetAccount
		if groupID != nil {
			groupAccount, err = LockBudgetAccount(ctx, db, projectID, budgetaccount.OwnerKindGroup, *groupID)
			if err != nil {
				return err
			}
			if first == nil {
				if _, err = db.APIKeyGroup.Query().Where(apikeygroup.IDEQ(*groupID), apikeygroup.ProjectIDEQ(projectID)).Only(ctx); err != nil {
					return err
				}
			}
		}
		now := time.Now().UTC()
		accounts := []*ent.BudgetAccount{keyAccount}
		if groupAccount != nil {
			accounts = append(accounts, groupAccount)
		}

		active := false
		usages := make(map[int]map[budget.Period]budget.Usage, len(accounts))
		// Check all global constraints before classifying a quote-specific rejection.
		for _, account := range accounts {
			usage, err := budgetUsage(ctx, db, account, now)
			if err != nil {
				return err
			}
			usages[account.ID] = usage
			active = active || !account.State.Policy.StartedAt.IsZero()
			blocked, err := budget.Check(now, string(account.OwnerKind), account.State.Policy, usage, 0)
			if err != nil {
				return err
			}
			if blocked != nil {
				return &BudgetDenied{Scope: blocked.Scope, Period: blocked.Period, Reason: blocked.Reason}
			}
		}
		for _, account := range accounts {
			if quoteErr != nil && account.State.HasLimits() {
				return &BudgetDenied{Scope: string(account.OwnerKind), Reason: "pricing_unavailable", CandidateOnly: true}
			}
			blocked, err := budget.Check(now, string(account.OwnerKind), account.State.Policy, usages[account.ID], estimate)
			if err != nil {
				return err
			}
			if blocked != nil {
				return &BudgetDenied{Scope: blocked.Scope, Period: blocked.Period, Reason: blocked.Reason, CandidateOnly: true}
			}
		}
		if !active {
			return nil
		}
		if estimate < 0 {
			return fmt.Errorf("negative budget reservation")
		}
		for _, account := range accounts {
			if account.State.Policy.StartedAt.IsZero() {
				continue
			}
			if account.Reserved > math.MaxInt64-int64(estimate) {
				return fmt.Errorf("budget reservation overflow")
			}
			if err := db.BudgetAccount.UpdateOneID(account.ID).SetState(account.State).AddReserved(int64(estimate)).Exec(ctx); err != nil {
				return err
			}
		}
		groupVersions := map[budget.Period]int64{}
		create := db.BudgetCharge.Create().SetProjectID(projectID).SetExecutionID(executionID).SetRequestID(requestID).SetKeyID(keyID).
			SetKeyAccountID(keyAccount.ID).SetEstimate(int64(estimate)).SetKeyVersions(chargeVersions(keyAccount.State)).SetCheckAfter(now.Add(time.Minute))
		if groupAccount != nil {
			create.SetGroupID(groupAccount.OwnerID).SetGroupAccountID(groupAccount.ID)
			groupVersions = chargeVersions(groupAccount.State)
		}
		return create.SetGroupVersions(groupVersions).Exec(ctx)
	})
}

func chargeVersions(state budget.State) map[budget.Period]int64 {
	versions := budgetVersions(state)
	if state.Policy.StartedAt.IsZero() {
		for period := range versions {
			versions[period] = -1
		}
	}
	return versions
}

// MarkBudgetSent runs immediately before invoking the transport, not at queue
// admission. A failure here prevents sending; an uncertain commit retains funds.
func (s *BudgetService) MarkSent(ctx context.Context, executionID int) error {
	return s.withCharge(ctx, executionID, func(ctx context.Context, db *ent.Client, charge *ent.BudgetCharge, _ []*ent.BudgetAccount) error {
		if charge.Status != budgetcharge.StatusPrepared {
			return fmt.Errorf("budget attempt is no longer prepared")
		}
		return db.BudgetCharge.UpdateOneID(charge.ID).SetStatus(budgetcharge.StatusSent).Exec(ctx)
	})
}

// EndAttempt releases only requests that provably never reached the transport.
// Sent requests retain their reservation; settlement callbacks may arrive later.
func (s *BudgetService) EndAttempt(ctx context.Context, executionID int) error {
	return s.withCharge(ctx, executionID, func(ctx context.Context, db *ent.Client, charge *ent.BudgetCharge, accounts []*ent.BudgetAccount) error {
		if charge.Status == budgetcharge.StatusSettled || charge.Status == budgetcharge.StatusReleased {
			return nil
		}
		if charge.Status != budgetcharge.StatusPrepared {
			return db.BudgetCharge.UpdateOneID(charge.ID).SetStatus(budgetcharge.StatusUnknown).SetCheckAfter(time.Now().UTC().Add(time.Minute)).Exec(ctx)
		}
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
			if err := db.BudgetAccount.UpdateOneID(account.ID).AddReserved(-charge.Estimate).Exec(ctx); err != nil {
				return err
			}
		}
		return db.BudgetCharge.UpdateOneID(charge.ID).SetStatus(budgetcharge.StatusReleased).Exec(ctx)
	})
}

func (s *BudgetService) withCharge(ctx context.Context, executionID int, fn func(context.Context, *ent.Client, *ent.BudgetCharge, []*ent.BudgetAccount) error) error {
	// Read only immutable attribution here. All authoritative mutable reads happen
	// again after locks in a new transaction; this lookup grants no spending.
	var identity *ent.BudgetCharge
	err := s.transaction(ctx, func(ctx context.Context, db *ent.Client) error {
		var err error
		identity, err = db.BudgetCharge.Query().Where(budgetcharge.ExecutionIDEQ(executionID)).Only(ctx)
		return err
	})
	if ent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.transaction(ctx, func(ctx context.Context, db *ent.Client) error {
		key, err := lockBudgetAccountID(ctx, db, identity.KeyAccountID)
		if err != nil {
			return err
		}
		accounts := []*ent.BudgetAccount{key}
		if identity.GroupAccountID != nil {
			group, err := lockBudgetAccountID(ctx, db, *identity.GroupAccountID)
			if err != nil {
				return err
			}
			accounts = append(accounts, group)
		}
		charge, err := db.BudgetCharge.Get(ctx, identity.ID)
		if err != nil {
			return err
		}
		return fn(ctx, db, charge, accounts)
	})
}
