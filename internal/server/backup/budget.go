package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"io"
	"math"

	"entgo.io/ent/dialect"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/ent/budgetchange"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/ent/budgetposting"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/project"
)

type BackupBudgetAccount struct {
	*ent.BudgetAccount
	ProjectName string `json:"project_name"`
	OwnerKey    string `json:"owner_key,omitempty"`
	GroupName   string `json:"group_name,omitempty"`
}

// A backup containing budgets must use one database snapshot for keys, groups,
// counters, reservations and postings. Independent table reads could restore a
// counter and reservation from opposite sides of a settlement.
func (svc *BackupService) doBackupToWriter(ctx context.Context, opts BackupOptions, w io.Writer) error {
	if !opts.IncludeAPIKeys {
		return svc.writeBackupToWriter(ctx, opts, w)
	}
	var tx *ent.Tx
	var err error
	if svc.db.Driver().Dialect() == dialect.SQLite {
		tx, err = svc.db.Tx(ctx)
	} else {
		tx, err = svc.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	}
	if err != nil {
		return err
	}
	defer tx.Rollback()
	copy := *svc
	copy.db = tx.Client()
	if err = copy.writeBackupToWriter(ctx, opts, w); err != nil {
		return err
	}
	return tx.Commit()
}
func (svc *BackupService) streamBudgets(ctx context.Context, o *objWriter, opts BackupOptions) error {
	if !opts.IncludeAPIKeys {
		return nil
	}
	err := streamArrayField(o, "budget_accounts", true, true, func(last int) ([]*ent.BudgetAccount, int, error) {
		rows, err := svc.db.BudgetAccount.Query().Where(budgetaccount.IDGT(last)).Order(ent.Asc(budgetaccount.FieldID)).Limit(backupBatchSize).All(ctx)
		if len(rows) > 0 {
			last = rows[len(rows)-1].ID
		}
		return rows, last, err
	}, func(account *ent.BudgetAccount) ([]byte, bool, error) {
		value := BackupBudgetAccount{BudgetAccount: account}
		ownerProject, err := svc.db.Project.Get(ctx, account.ProjectID)
		if err != nil && !ent.IsNotFound(err) {
			return nil, false, err
		}
		if ownerProject != nil {
			value.ProjectName = ownerProject.Name
		}
		if account.OwnerKind == budgetaccount.OwnerKindKey {
			key, err := svc.db.APIKey.Get(ctx, account.OwnerID)
			if err != nil && !ent.IsNotFound(err) {
				return nil, false, err
			}
			if key != nil {
				value.OwnerKey = key.Key
			}
		} else {
			group, err := svc.db.APIKeyGroup.Get(ctx, account.OwnerID)
			if err != nil && !ent.IsNotFound(err) {
				return nil, false, err
			}
			if group != nil {
				value.GroupName = group.Name
			}
		}
		data, err := json.Marshal(value)
		return data, true, err
	})
	if err != nil {
		return err
	}
	if err = streamArrayField(o, "budget_charges", true, true, func(last int) ([]*ent.BudgetCharge, int, error) {
		rows, err := svc.db.BudgetCharge.Query().Where(budgetcharge.IDGT(last)).Order(ent.Asc(budgetcharge.FieldID)).Limit(backupBatchSize).All(ctx)
		if len(rows) > 0 {
			last = rows[len(rows)-1].ID
		}
		return rows, last, err
	}, func(value *ent.BudgetCharge) ([]byte, bool, error) {
		if value.AsyncTask != nil {
			ch, err := svc.db.Channel.Get(ctx, value.AsyncTask.ChannelID)
			if err != nil && !ent.IsNotFound(err) {
				return nil, false, err
			}
			if ch != nil {
				value.AsyncTask.ChannelName = ch.Name
			}
		}
		data, err := json.Marshal(value)
		return data, true, err
	}); err != nil {
		return err
	}
	if err = streamArrayField(o, "budget_postings", true, true, func(last int) ([]*ent.BudgetPosting, int, error) {
		rows, err := svc.db.BudgetPosting.Query().Where(budgetposting.IDGT(last)).Order(ent.Asc(budgetposting.FieldID)).Limit(backupBatchSize).All(ctx)
		if len(rows) > 0 {
			last = rows[len(rows)-1].ID
		}
		return rows, last, err
	}, func(value *ent.BudgetPosting) ([]byte, bool, error) {
		data, err := json.Marshal(value)
		return data, true, err
	}); err != nil {
		return err
	}
	return streamArrayField(o, "budget_changes", true, true, func(last int) ([]*ent.BudgetChange, int, error) {
		rows, err := svc.db.BudgetChange.Query().Where(budgetchange.IDGT(last)).Order(ent.Asc(budgetchange.FieldID)).Limit(backupBatchSize).All(ctx)
		if len(rows) > 0 {
			last = rows[len(rows)-1].ID
		}
		return rows, last, err
	}, func(value *ent.BudgetChange) ([]byte, bool, error) {
		data, err := json.Marshal(value)
		return data, true, err
	})
}

func (svc *BackupService) restoreBudgets(ctx context.Context, db *ent.Client, data BackupData) error {
	accounts := map[int]*ent.BudgetAccount{}
	if err := validateBudgetBackup(data); err != nil {
		return err
	}
	detachedProjects := map[int]int{}
	nextProject := -1
	if row, err := db.BudgetAccount.Query().Order(ent.Asc(budgetaccount.FieldProjectID)).First(ctx); err == nil && row.ProjectID < 0 {
		nextProject = row.ProjectID - 1
	} else if err != nil && !ent.IsNotFound(err) {
		return err
	}
	// Negative owner/execution identifiers identify detached historical records.
	// They never collide with newly allocated live API keys or executions.
	nextOwner := -1
	if row, err := db.BudgetAccount.Query().Order(ent.Asc(budgetaccount.FieldOwnerID)).First(ctx); err == nil && row.OwnerID < 0 {
		nextOwner = row.OwnerID - 1
	} else if err != nil && !ent.IsNotFound(err) {
		return err
	}
	for _, source := range data.BudgetAccounts {
		if source == nil || source.BudgetAccount == nil {
			return fmt.Errorf("invalid budget account in backup")
		}
		var projectID int
		if source.ProjectName != "" {
			targetProject, err := db.Project.Query().Where(project.NameEQ(source.ProjectName)).Only(ctx)
			if err != nil {
				return fmt.Errorf("restore budget project %q: %w", source.ProjectName, err)
			}
			projectID = targetProject.ID
		} else {
			projectID = detachedProjects[source.ProjectID]
			if projectID == 0 {
				projectID = nextProject
				nextProject--
				detachedProjects[source.ProjectID] = projectID
			}
		}
		ownerID := nextOwner
		nextOwner--
		if projectID > 0 && source.OwnerKind == budgetaccount.OwnerKindKey && source.OwnerKey != "" {
			key, err := db.APIKey.Query().Where(apikey.KeyEQ(source.OwnerKey), apikey.ProjectIDEQ(projectID)).Only(ctx)
			if err != nil {
				return fmt.Errorf("restore budget key: %w", err)
			}
			ownerID = key.ID
		} else if projectID > 0 && source.OwnerKind == budgetaccount.OwnerKindGroup && source.GroupName != "" {
			groupID, err := restoreKeyGroup(ctx, db, projectID, source.GroupName)
			if err != nil {
				return err
			}
			ownerID = *groupID
		}
		existing, err := db.BudgetAccount.Query().Where(budgetaccount.ProjectIDEQ(projectID), budgetaccount.OwnerKindEQ(source.OwnerKind), budgetaccount.OwnerIDEQ(ownerID)).Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		if existing != nil {
			return fmt.Errorf("budget history already exists for %s %d; restore into an empty budget target", source.OwnerKind, ownerID)
		}
		if err = source.State.Policy.Validate(); err != nil {
			return err
		}
		account, err := db.BudgetAccount.Create().SetProjectID(projectID).SetOwnerKind(source.OwnerKind).SetOwnerID(ownerID).SetState(source.State).SetReserved(source.Reserved).SetRevision(source.Revision).SetCreatedAt(source.CreatedAt).Save(ctx)
		if err != nil {
			return err
		}
		accounts[source.ID] = account
	}
	charges := map[int]int{}
	nextExecution := -1
	if row, err := db.BudgetCharge.Query().Order(ent.Asc(budgetcharge.FieldExecutionID)).First(ctx); err == nil && row.ExecutionID < 0 {
		nextExecution = row.ExecutionID - 1
	} else if err != nil && !ent.IsNotFound(err) {
		return err
	}
	for _, source := range data.BudgetCharges {
		if source == nil {
			return fmt.Errorf("invalid budget charge in backup")
		}
		key := accounts[source.KeyAccountID]
		if key == nil {
			return fmt.Errorf("budget charge key account is missing")
		}
		create := db.BudgetCharge.Create().SetProjectID(key.ProjectID).SetExecutionID(nextExecution).SetRequestID(nextExecution).SetKeyID(key.OwnerID).SetKeyAccountID(key.ID).
			SetEstimate(source.Estimate).SetNillableActual(source.Actual).SetKeyVersions(source.KeyVersions).SetGroupVersions(source.GroupVersions).SetStatus(source.Status).SetCheckAfter(source.CheckAfter).SetNillableSettledAt(source.SettledAt).SetCreatedAt(source.CreatedAt)
		nextExecution--
		if source.AsyncTask != nil {
			task := *source.AsyncTask
			task.ChannelID = 0
			if task.ChannelName != "" {
				ch, err := db.Channel.Query().Where(channel.NameEQ(task.ChannelName)).Only(ctx)
				needsPoll := source.Actual == nil && source.Status != budgetcharge.StatusSettled && source.Status != budgetcharge.StatusReleased
				if err != nil && (!ent.IsNotFound(err) || needsPoll) {
					return fmt.Errorf("restore pending budget task requires channel %q: %w", task.ChannelName, err)
				}
				if ch != nil {
					task.ChannelID = ch.ID
				}
			}
			create.SetAsyncTask(&task)
		}
		if source.Usage != nil {
			create.SetUsage(source.Usage)
		}
		if source.CostSnapshot != nil {
			create.SetCostSnapshot(source.CostSnapshot)
		}
		if source.GroupAccountID != nil {
			group := accounts[*source.GroupAccountID]
			if group == nil || group.ProjectID != key.ProjectID {
				return fmt.Errorf("budget charge group account is missing")
			}
			create.SetGroupAccountID(group.ID).SetGroupID(group.OwnerID)
		}
		charge, err := create.Save(ctx)
		if err != nil {
			return err
		}
		charges[source.ID] = charge.ID
	}
	for _, source := range data.BudgetPostings {
		if source == nil {
			return fmt.Errorf("invalid budget posting in backup")
		}
		account := accounts[source.AccountID]
		chargeID := charges[source.ChargeID]
		if account == nil || chargeID == 0 {
			return fmt.Errorf("budget posting references are missing")
		}
		if err := db.BudgetPosting.Create().SetProjectID(account.ProjectID).SetAccountID(account.ID).SetChargeID(chargeID).SetAmount(source.Amount).SetSettledAt(source.SettledAt).
			SetFiveHourVersion(source.FiveHourVersion).SetDailyVersion(source.DailyVersion).SetWeeklyVersion(source.WeeklyVersion).SetMonthlyVersion(source.MonthlyVersion).SetTotalVersion(source.TotalVersion).Exec(ctx); err != nil {
			return err
		}
	}
	for _, source := range data.BudgetChanges {
		if source == nil {
			return fmt.Errorf("invalid budget audit in backup")
		}
		account := accounts[source.AccountID]
		if account == nil {
			return fmt.Errorf("budget audit account is missing")
		}
		if err := db.BudgetChange.Create().SetProjectID(account.ProjectID).SetAccountID(account.ID).SetActorID(-absBudgetActor(source.ActorID)).SetOperationID(source.OperationID).SetOperation(source.Operation).
			SetBefore(source.Before).SetAfter(source.After).SetCreatedAt(source.CreatedAt).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Reject incomplete snapshots rather than restoring apparently spendable funds.
func validateBudgetBackup(data BackupData) error {
	accounts := map[int]*ent.BudgetAccount{}
	reserved := map[int]int64{}
	charges := map[int]*ent.BudgetCharge{}
	for _, a := range data.BudgetAccounts {
		if a == nil || a.BudgetAccount == nil || a.Reserved < 0 || accounts[a.ID] != nil {
			return fmt.Errorf("invalid budget account in backup")
		}
		accounts[a.ID] = a.BudgetAccount
	}
	for _, c := range data.BudgetCharges {
		if c == nil || c.Estimate < 0 || (c.Actual != nil && *c.Actual < 0) || charges[c.ID] != nil {
			return fmt.Errorf("invalid budget charge in backup")
		}
		charges[c.ID] = c
		key := accounts[c.KeyAccountID]
		if key == nil || key.OwnerKind != budgetaccount.OwnerKindKey || key.ProjectID != c.ProjectID {
			return fmt.Errorf("invalid budget key attribution")
		}
		ids := []int{c.KeyAccountID}
		if c.GroupAccountID != nil {
			group := accounts[*c.GroupAccountID]
			if group == nil || group.OwnerKind != budgetaccount.OwnerKindGroup || group.ProjectID != c.ProjectID {
				return fmt.Errorf("invalid budget group attribution")
			}
			ids = append(ids, group.ID)
		}
		if c.Status != budgetcharge.StatusPrepared && c.Status != budgetcharge.StatusSent && c.Status != budgetcharge.StatusUnknown {
			continue
		}
		for _, id := range ids {
			versions := c.KeyVersions
			if id != c.KeyAccountID {
				versions = c.GroupVersions
			}
			if versions[budget.Total] < 0 {
				continue
			}
			if reserved[id] > math.MaxInt64-c.Estimate {
				return fmt.Errorf("budget reservation overflow in backup")
			}
			reserved[id] += c.Estimate
		}
	}
	for id, a := range accounts {
		if a.Reserved != reserved[id] {
			return fmt.Errorf("budget reservation snapshot is inconsistent")
		}
	}
	for _, p := range data.BudgetPostings {
		if p == nil || p.Amount < 0 {
			return fmt.Errorf("invalid budget posting in backup")
		}
		c := charges[p.ChargeID]
		if c == nil || (c.KeyAccountID != p.AccountID && (c.GroupAccountID == nil || *c.GroupAccountID != p.AccountID)) {
			return fmt.Errorf("invalid budget posting attribution")
		}
	}
	return nil
}

// Negative actor IDs explicitly belong to the source instance, never a live user.
func absBudgetActor(id int) int {
	if id < 0 {
		return -id
	}
	return id
}
