package backup

import (
	"context"
	"encoding/json"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/samber/lo"
)

type BackupUsageArchive struct {
	ent.UsageArchive
	ProjectName      string              `json:"project_name,omitempty"`
	ChannelName      string              `json:"channel_name,omitempty"`
	APIKeyKey        string              `json:"api_key_key,omitempty"`
	BillingGroupName string              `json:"billing_group_name,omitempty"`
	Parent           *BackupUsageRequest `json:"parent,omitempty"`
}

type billIdentity struct{ project, request, usage int }

// Old bills can be attributed only through a settled charge for that exact bill.
func attachLegacyBilling[T any](ctx context.Context, db *ent.Client, rows []T, identity func(T) billIdentity, apply func(T, int)) error {
	ids := make([]int, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, identity(r).usage)
	}
	if len(ids) == 0 {
		return nil
	}
	charges, err := db.BudgetCharge.Query().Where(budgetcharge.UsageLogIDIn(ids...), budgetcharge.GroupIDNotNil(), budgetcharge.StatusEQ(budgetcharge.StatusSettled)).Select(budgetcharge.FieldProjectID, budgetcharge.FieldRequestID, budgetcharge.FieldUsageLogID, budgetcharge.FieldGroupID).All(ctx)
	if err != nil {
		return err
	}
	owners := map[billIdentity]int{}
	for _, c := range charges {
		owners[billIdentity{c.ProjectID, c.RequestID, *c.UsageLogID}] = *c.GroupID
	}
	for _, r := range rows {
		if g, ok := owners[identity(r)]; ok {
			apply(r, g)
		}
	}
	return nil
}

func recoverLegacyBackupBilling(data BackupData) {
	groups := map[[2]int]string{}
	for _, a := range data.BudgetAccounts {
		if a != nil && a.BudgetAccount != nil && a.OwnerKind == budgetaccount.OwnerKindGroup {
			groups[[2]int{a.ProjectID, a.OwnerID}] = a.GroupName
		}
	}
	charges := map[billIdentity]int{}
	for _, c := range data.BudgetCharges {
		if c != nil && c.Status == budgetcharge.StatusSettled && c.GroupID != nil && c.UsageLogID != nil {
			charges[billIdentity{c.ProjectID, c.RequestID, *c.UsageLogID}] = *c.GroupID
		}
	}
	for _, l := range data.UsageLogs {
		if l == nil || l.BillingGroupID != nil {
			continue
		}
		if g, ok := charges[billIdentity{l.ProjectID, l.RequestID, l.ID}]; ok {
			l.BillingGroupID = &g
			l.BillingGroupName = groups[[2]int{l.ProjectID, g}]
			l.BilledCost = l.TotalCost
		}
	}
}

func (svc *BackupService) streamUsageArchives(ctx context.Context, o *objWriter, opts BackupOptions, keys, groups map[int]string) error {
	if !opts.IncludeUsageStats {
		return nil
	}
	db := svc.db
	projects, err := db.Project.Query().Select(project.FieldName).All(ctx)
	if err != nil {
		return err
	}
	channels, err := db.Channel.Query().Select(channel.FieldName).All(ctx)
	if err != nil {
		return err
	}
	projectNames := lo.SliceToMap(projects, func(p *ent.Project) (int, string) { return p.ID, p.Name })
	channelNames := lo.SliceToMap(channels, func(c *ent.Channel) (int, string) { return c.ID, c.Name })
	var live map[int]bool
	var shells map[int]*BackupUsageRequest
	return streamArrayField(o, "usage_archives", true, true, func(last int) ([]*ent.UsageArchive, int, error) {
		rows, err := db.UsageArchive.Query().Where(usagearchive.IDGT(last)).Order(ent.Asc(usagearchive.FieldID)).Limit(backupBatchSize).All(ctx)
		if err != nil || len(rows) == 0 {
			return nil, 0, err
		}
		ids, requests, bills := []int{}, []int{}, []*ent.UsageArchive{}
		for _, r := range rows {
			requests = append(requests, r.RequestID)
			if r.Kind == usagearchive.KindUsage {
				ids = append(ids, r.SourceID)
				bills = append(bills, r)
			}
		}
		existing, err := db.UsageLog.Query().Where(usagelog.IDIn(ids...)).IDs(ctx)
		if err != nil {
			return nil, 0, err
		}
		live = map[int]bool{}
		for _, id := range existing {
			live[id] = true
		}
		parents, err := db.Request.Query().Where(request.IDIn(requests...)).Select(request.FieldRestoreRef, request.FieldProjectID, request.FieldChannelID, request.FieldAPIKeyID, request.FieldModelID, request.FieldFormat, request.FieldSource, request.FieldStatus, request.FieldStream, request.FieldCreatedAt, request.FieldUpdatedAt).All(ctx)
		if err != nil {
			return nil, 0, err
		}
		shells = map[int]*BackupUsageRequest{}
		for _, r := range parents {
			shells[r.ID] = &BackupUsageRequest{Request: *r, ProjectName: projectNames[r.ProjectID], ChannelName: channelNames[r.ChannelID], APIKeyKey: keys[r.APIKeyID]}
		}
		err = attachLegacyBilling(ctx, db, bills, func(r *ent.UsageArchive) billIdentity { return billIdentity{r.ProjectID, r.RequestID, r.SourceID} }, func(r *ent.UsageArchive, g int) {
			if r.BillingGroupID == nil {
				r.BillingGroupID = &g
				r.BilledCost = r.TotalCost
			}
		})
		return rows, rows[len(rows)-1].ID, err
	}, func(r *ent.UsageArchive) ([]byte, bool, error) {
		if r.Kind == usagearchive.KindUsage && live[r.SourceID] {
			return nil, false, nil
		}
		data := BackupUsageArchive{UsageArchive: *r, ProjectName: projectNames[r.ProjectID], ChannelName: channelNames[r.ChannelID], APIKeyKey: keys[r.APIKeyID], BillingGroupName: groups[lo.FromPtr(r.BillingGroupID)], Parent: shells[r.RequestID]}
		b, err := json.Marshal(data)
		return b, true, err
	})
}

func archiveRestoreRef(r *BackupUsageArchive) string {
	return objects.UsageRestoreRef(r.RestoreRef, string(r.Kind), r.ProjectID, r.SourceID, r.OccurredAt)
}

func archiveBackupLog(l *BackupUsageLog) *BackupUsageArchive {
	r := &l.UsageLog
	return &BackupUsageArchive{
		UsageArchive: ent.UsageArchive{
			Kind: usagearchive.KindUsage, RestoreRef: r.RestoreRef, SourceID: r.ID,
			ProjectID: r.ProjectID, RequestID: r.RequestID, ChannelID: r.ChannelID, ModelID: r.ModelID, OccurredAt: r.CreatedAt,
			PromptTokens: r.PromptTokens, CompletionTokens: r.CompletionTokens, TotalTokens: r.TotalTokens,
			PromptCachedTokens: r.PromptCachedTokens, PromptWriteCachedTokens: r.PromptWriteCachedTokens, CompletionReasoningTokens: r.CompletionReasoningTokens,
			TotalCost: r.TotalCost, BillingGroupID: r.BillingGroupID, BilledCost: r.BilledCost, BillingEstimated: r.BillingEstimated, BillingEvidence: biz.UsageBillingEvidence(r),
		},
		ProjectName: l.ProjectName, ChannelName: l.ChannelName, APIKeyKey: l.APIKeyKey, BillingGroupName: l.BillingGroupName,
	}
}
