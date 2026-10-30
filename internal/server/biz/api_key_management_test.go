package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/internal/pkg/xcache"
)

func TestKeyManagementAtomicRowsAndReplay(t *testing.T) {
	s, db := setupTestAPIKeyService(t, xcache.Config{Mode: xcache.ModeMemory})
	t.Cleanup(s.Stop)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), db))
	owner := db.User.Create().SetEmail("management@example.test").SetPassword("fixture").SetIsOwner(true).SaveX(ctx)
	p := db.Project.Create().SetName("management").SaveX(ctx)
	ctx = contexts.WithProjectID(contexts.WithUser(ctx, owner), p.ID)
	name, badName := "Recipient A", "Rollback"
	badRevision := 99
	patch := &KeyBudgetPatch{Rules: []*BudgetRuleInput{{Period: budget.Daily, Mode: budget.Fixed, Limit: "10", Minute: 0}}}
	rows := []*KeyManagementRow{
		{RowID: "a", Action: "createGroup", Name: &name, Budget: patch, Keys: []*ManagedKeyInput{{Name: "Codex", Budget: patch}, {Name: "Claude"}}},
		{RowID: "bad", Action: "createGroup", Name: &badName, Budget: patch, Keys: []*ManagedKeyInput{{Name: "first", Budget: patch}, {Name: "second", Budget: &KeyBudgetPatch{Revision: &badRevision}}}},
	}
	results, err := s.ApplyKeyManagement(ctx, nil, "batch-1", rows)
	require.NoError(t, err)
	require.True(t, results[0].Success, results[0].Error)
	require.False(t, results[1].Success)
	require.Len(t, results[0].KeyIDs, 2)
	require.False(t, db.APIKeyGroup.Query().Where(apikeygroup.NameEQ(badName)).ExistX(ctx))
	require.False(t, db.APIKey.Query().Where(apikey.NameEQ("first")).ExistX(ctx))
	require.Equal(t, 2, db.BudgetAccount.Query().CountX(ctx), "失败行已写入的分组和密钥预算也必须回滚")
	replayed, err := s.ApplyKeyManagement(ctx, nil, "batch-1", rows[:1])
	require.NoError(t, err)
	require.Equal(t, results[0], replayed[0])
	receipts, err := s.KeyManagementResults(ctx, "batch-1")
	require.NoError(t, err)
	require.Len(t, receipts, 1)
	view, err := s.Budget(ctx, "group", results[0].GroupID)
	require.NoError(t, err)
	require.Equal(t, "10.00000000", view.Windows[1].Limit)
	groups, err := s.ManagedKeyGroups(ctx, "Codex", "all", 0, 20, true)
	require.NoError(t, err)
	require.Len(t, groups.Items, 1)
	require.Equal(t, 2, groups.Items[0].KeyCount)
	for i, window := range view.Windows {
		actual := groups.Items[0].Budget.Windows[i]
		require.Equal(t, window.Period, actual.Period)
		require.Equal(t, window.Limit, actual.Limit)
		require.Equal(t, window.Settled, actual.Settled)
		require.Equal(t, window.Reserved, actual.Reserved)
		require.Equal(t, window.Available, actual.Available)
	}
	key, err := s.Budget(ctx, "key", results[0].KeyIDs[0])
	require.NoError(t, err)
	require.Equal(t, "10.00000000", key.Windows[1].Limit)
	// A reused id with altered input cannot be used to create a second group.
	name = "Changed"
	rejected, err := s.ApplyKeyManagement(ctx, nil, "batch-1", rows[:1])
	require.NoError(t, err)
	require.False(t, rejected[0].Success)
	group := db.APIKeyGroup.GetX(ctx, results[0].GroupID)
	disabled, enabled := false, true
	member := db.APIKey.GetX(ctx, results[0].KeyIDs[0])
	member.Update().SetStatus(apikey.StatusDisabled).SaveX(ctx)
	update := &KeyManagementRow{RowID: "disable", Action: "updateGroup", TargetID: &group.ID, Revision: &group.Revision, Enabled: &disabled}
	out, err := s.ApplyKeyManagement(ctx, nil, "status", []*KeyManagementRow{update})
	require.NoError(t, err)
	require.True(t, out[0].Success, out[0].Error)
	require.ErrorIs(t, s.CheckAPIKeyGroupEnabled(ctx, member.ID, p.ID), ErrInvalidAPIKey)
	group = db.APIKeyGroup.GetX(ctx, group.ID)
	update.RowID = "enable"
	update.Revision = &group.Revision
	update.Enabled = &enabled
	out, err = s.ApplyKeyManagement(ctx, nil, "status", []*KeyManagementRow{update})
	require.NoError(t, err)
	require.True(t, out[0].Success, out[0].Error)
	require.NoError(t, s.CheckAPIKeyGroupEnabled(ctx, member.ID, p.ID))
	require.Equal(t, apikey.StatusDisabled, db.APIKey.GetX(ctx, member.ID).Status)
}

func TestManagedGroupBudgetSummaryMatchesLedger(t *testing.T) {
	f := newBudgetFixture(t)
	f.configure(t, "group", f.group.ID, "5")
	require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 21, 21, budgetAmount(t, "2"), nil))
	cost := budgetAmount(t, "2")
	_, _, settleErr := f.service.Settle(f.ctx, 21, &cost, nil)
	require.NoError(t, settleErr)
	require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 22, 22, budgetAmount(t, "1"), nil))
	view, err := f.admin.Budget(f.ctx, "group", f.group.ID)
	require.NoError(t, err)
	views, err := f.admin.groupBudgetViews(f.ctx, f.key.ProjectID, []int{f.group.ID}, time.Now().UTC())
	require.NoError(t, err)
	for i, w := range view.Windows {
		actual := views[f.group.ID].Windows[i]
		require.Equal(t, w.Settled, actual.Settled)
		require.Equal(t, w.Reserved, actual.Reserved)
		require.Equal(t, w.Available, actual.Available)
	}
	group := f.db.APIKeyGroup.GetX(f.ctx, f.group.ID)
	result, err := f.admin.ApplyKeyManagement(f.ctx, nil, "patch-daily", []*KeyManagementRow{{RowID: "g", Action: "updateGroup", TargetID: &group.ID, Revision: &group.Revision, Budget: &KeyBudgetPatch{Revision: &view.Revision, Rules: []*BudgetRuleInput{{Period: budget.Daily, Mode: budget.Fixed, Limit: "10"}}}}})
	require.NoError(t, err)
	require.True(t, result[0].Success, result[0].Error)
	after, err := f.admin.Budget(f.ctx, "group", group.ID)
	require.NoError(t, err)
	for i, w := range after.Windows {
		require.Equal(t, view.Windows[i].Settled, w.Settled)
		require.Equal(t, view.Windows[i].Reserved, w.Reserved)
		if w.Period != budget.Daily {
			require.Equal(t, view.Windows[i].Limit, w.Limit)
		}
	}
}

func TestKeyManagementTemplateAndAccess(t *testing.T) {
	f := newBudgetFixture(t)
	templates := NewAPIKeyProfileTemplateService(APIKeyProfileTemplateServiceParams{Ent: f.db})
	template, err := templates.CreateTemplate(f.ctx, ent.CreateAPIKeyProfileTemplateInput{Name: "recipient-template", ProjectID: f.key.ProjectID}, &objects.APIKeyProfile{Name: "recipient-template"})
	require.NoError(t, err)
	rows := []*KeyManagementRow{{RowID: "k", Action: "createKey", GroupRef: &f.group.ID, Keys: []*ManagedKeyInput{{Name: "templated", TemplateID: &template.ID, AllowedIPs: []string{"127.0.0.1/32"}}}}}
	result, err := f.admin.ApplyKeyManagement(f.ctx, templates, "template", rows)
	require.NoError(t, err)
	require.True(t, result[0].Success, result[0].Error)
	key := f.db.APIKey.GetX(f.ctx, result[0].KeyIDs[0])
	require.Equal(t, "recipient-template", key.Profiles.ActiveProfile)
	require.Equal(t, []string{"127.0.0.1/32"}, key.AllowedIps)
	member := f.db.User.Create().SetEmail("member@example.test").SetPassword("fixture").SaveX(f.ctx)
	_, err = f.admin.ApplyKeyManagement(contexts.WithUser(f.ctx, member), templates, "denied", rows)
	require.Error(t, err)
	otherOwner := f.db.User.Create().SetEmail("other-owner@example.test").SetPassword("fixture").SetIsOwner(true).SaveX(f.ctx)
	receipts, err := f.admin.KeyManagementResults(contexts.WithUser(f.ctx, otherOwner), "template")
	require.NoError(t, err)
	require.Empty(t, receipts)
	other := f.db.Project.Create().SetName("foreign").SaveX(f.ctx)
	result, err = f.admin.ApplyKeyManagement(contexts.WithProjectID(f.ctx, other.ID), templates, "foreign", rows)
	require.NoError(t, err)
	require.False(t, result[0].Success)
}

func TestAPIKeyGroupSuspensionWithCachedCredential(t *testing.T) {
	f := newBudgetFixture(t)
	auth := &AuthService{APIKeyService: f.admin}
	_, err := auth.AuthenticateAPIKey(f.ctx, f.key.Key)
	require.NoError(t, err)
	f.group.Update().SetEnabled(false).SaveX(f.ctx)
	_, err = auth.AuthenticateAPIKey(f.ctx, f.key.Key)
	require.ErrorIs(t, err, ErrInvalidAPIKey)
	// Fresh membership is authoritative even when the credential cache is warm.
	f.key.Update().ClearGroupRef().SaveX(f.ctx)
	_, err = auth.AuthenticateAPIKey(f.ctx, f.key.Key)
	require.NoError(t, err)
}

func TestKeyManagementDestinationConflictsAndVersion(t *testing.T) {
	f := newBudgetFixture(t)
	other := f.db.APIKeyGroup.Create().SetProjectID(f.key.ProjectID).SetName("other").SaveX(f.ctx)
	key := f.db.APIKey.Create().SetProjectID(f.key.ProjectID).SetName(f.key.Name).SetUserID(f.key.UserID).SetKey("other-fixture").SetGroupRef(other.ID).SaveX(f.ctx)
	deletes := []*KeyManagementRow{{RowID: "a", Action: "deleteGroup", TargetID: &f.group.ID, Revision: &f.group.Revision}, {RowID: "b", Action: "deleteGroup", TargetID: &other.ID, Revision: &other.Revision}}
	preview, err := f.admin.PreviewKeyManagement(f.ctx, deletes)
	require.NoError(t, err)
	require.True(t, preview[0].Success)
	require.False(t, preview[1].Success)
	result, err := f.admin.ApplyKeyManagement(f.ctx, nil, "delete-conflict", deletes)
	require.NoError(t, err)
	require.True(t, result[0].Success, result[0].Error)
	require.False(t, result[1].Success)
	require.Equal(t, other.ID, *f.db.APIKey.GetX(f.ctx, key.ID).GroupRef)
	newName := "changed"
	update := &KeyManagementRow{RowID: "key", Action: "updateKey", TargetID: &key.ID, UpdatedAt: &key.UpdatedAt, Name: &newName}
	result, err = f.admin.ApplyKeyManagement(f.ctx, nil, "rename-key", []*KeyManagementRow{update})
	require.NoError(t, err)
	require.True(t, result[0].Success, result[0].Error)
	result, err = f.admin.ApplyKeyManagement(f.ctx, nil, "stale-key", []*KeyManagementRow{update})
	require.NoError(t, err)
	require.False(t, result[0].Success)
}

func TestManagedGroupPaginationAndAvailability(t *testing.T) {
	f := newBudgetFixture(t)
	other := f.db.APIKeyGroup.Create().SetProjectID(f.key.ProjectID).SetName("second").SaveX(f.ctx)
	first, err := f.admin.ManagedKeyGroups(f.ctx, "", "all", 0, 1, false)
	require.NoError(t, err)
	second, err := f.admin.ManagedKeyGroups(f.ctx, "", "all", 1, 1, false)
	require.NoError(t, err)
	require.Equal(t, 2, first.Total)
	require.Len(t, first.Items, 1)
	require.Len(t, second.Items, 1)
	require.NotEqual(t, first.Items[0].ID, second.Items[0].ID)
	// 已暂占全部额度的分组不能出现在“可用”筛选中。
	f.configure(t, "group", f.group.ID, "5")
	require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 31, 31, budgetAmount(t, "5"), nil))
	available, err := f.admin.ManagedKeyGroups(f.ctx, "", "enabled", 0, 20, false)
	require.NoError(t, err)
	require.Len(t, available.Items, 1)
	require.Equal(t, other.ID, available.Items[0].ID)
	exhausted, err := f.admin.ManagedKeyGroups(f.ctx, "", "exhausted", 0, 20, false)
	require.NoError(t, err)
	require.Len(t, exhausted.Items, 1)
	require.Equal(t, f.group.ID, exhausted.Items[0].ID)
	other.Update().SetEnabled(false).SaveX(f.ctx)
	available, err = f.admin.ManagedKeyGroups(f.ctx, "", "enabled", 0, 20, false)
	require.NoError(t, err)
	require.Empty(t, available.Items)
}

func TestKeyManagementBudgetOperationActorIsolation(t *testing.T) {
	f := newBudgetFixture(t)
	other := f.db.User.Create().SetEmail("budget-actor@example.test").SetPassword("fixture").SetIsOwner(true).SaveX(f.ctx)
	owner, _ := contexts.GetUser(f.ctx)
	for i, actor := range []*ent.User{owner, other} {
		ctx := contexts.WithProjectID(contexts.WithUser(authz.WithTestBypass(ent.NewContext(context.Background(), f.db)), actor), f.key.ProjectID)
		name := []string{"actor-a", "actor-b"}[i]
		result, err := f.admin.ApplyKeyManagement(ctx, nil, "same-operation", []*KeyManagementRow{{RowID: "same-row", Action: "createGroup", Name: &name,
			Budget: &KeyBudgetPatch{Rules: []*BudgetRuleInput{{Period: budget.Daily, Mode: budget.Fixed, Limit: "10"}}}}})
		require.NoError(t, err)
		require.True(t, result[0].Success, result[0].Error)
	}
}

func TestKeyManagementAuthorizationWithoutBypass(t *testing.T) {
	f := newBudgetFixture(t)
	member := f.db.User.Create().SetEmail("no-management-access@example.test").SetPassword("fixture").SaveX(f.ctx)
	ctx := contexts.WithProjectID(contexts.WithUser(ent.NewContext(context.Background(), f.db), member), f.key.ProjectID)
	name := "must-not-be-created"
	rows := []*KeyManagementRow{{RowID: "denied", Action: "createGroup", Name: &name}}
	_, err := f.admin.PreviewKeyManagement(ctx, rows)
	require.Error(t, err)
	_, err = f.admin.ApplyKeyManagement(ctx, nil, "denied", rows)
	require.Error(t, err)
	_, err = f.admin.KeyManagementResults(ctx, "denied")
	require.Error(t, err)
	_, err = f.admin.ManagedKeyGroups(ctx, "", "all", 0, 20, false)
	require.Error(t, err)
	_, err = f.admin.ManagedGroupKeys(ctx, f.group.ID, 0, 20)
	require.Error(t, err)
	require.False(t, f.db.APIKeyGroup.Query().Where(apikeygroup.NameEQ(name)).ExistX(f.ctx))
}
