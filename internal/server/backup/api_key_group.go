package backup

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikeychannelrate"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
	"github.com/looplj/axonhub/internal/ent/apikeygroupchannelrate"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/objects"
)

func restoreKeyGroup(ctx context.Context, db *ent.Client, projectID int, name string) (*int, error) {
	if name == "" {
		return nil, nil
	}
	group, err := db.APIKeyGroup.Query().Where(apikeygroup.ProjectIDEQ(projectID), apikeygroup.NameEQ(name)).Only(ctx)
	if ent.IsNotFound(err) {
		group, err = db.APIKeyGroup.Create().SetProjectID(projectID).SetName(name).Save(ctx)
	}
	if err != nil {
		return nil, err
	}
	return &group.ID, nil
}

func (svc *BackupService) streamAPIKeyGroups(ctx context.Context, out *objWriter, opts BackupOptions) error {
	return streamArrayField(out, "api_key_groups", opts.IncludeAPIKeys, true,
		func(last int) ([]*ent.APIKeyGroup, int, error) {
			groups, err := svc.db.APIKeyGroup.Query().Where(apikeygroup.IDGT(last)).WithProject().WithChannelRates(func(q *ent.APIKeyGroupChannelRateQuery) { q.WithChannel() }).Order(ent.Asc(apikeygroup.FieldID)).Limit(backupBatchSize).All(ctx)
			if len(groups) > 0 {
				last = groups[len(groups)-1].ID
			}
			return groups, last, err
		}, func(group *ent.APIKeyGroup) ([]byte, bool, error) {
			projectName := ""
			if group.Edges.Project != nil {
				projectName = group.Edges.Project.Name
			}
			rates := []BackupChannelRate{}
			for _, r := range group.Edges.ChannelRates {
				if c := r.Edges.Channel; c != nil {
					rates = append(rates, BackupChannelRate{r.ChannelID, c.Name, r.Multiplier})
				}
			}
			data, err := json.Marshal(BackupAPIKeyGroup{GlobalRate: group.GlobalRate, ChannelRates: rates, ProjectName: projectName, Name: group.Name, Note: group.Note, Enabled: group.Enabled, ShowChannelInSelfUsage: group.ShowChannelInSelfUsage})
			return data, true, err
		})
}

func (svc *BackupService) restoreAPIKeyGroups(ctx context.Context, db *ent.Client, groups []*BackupAPIKeyGroup, strategy ConflictStrategy, channels *channelRestoreState) error {
	for _, source := range groups {
		if source == nil {
			continue
		}
		target, err := db.Project.Query().Where(project.NameEQ(source.ProjectName)).Only(ctx)
		if ent.IsNotFound(err) {
			continue // 与密钥恢复一致：未选择恢复且不存在的项目不创建。
		}
		if err != nil {
			return err
		}
		existing, err := db.APIKeyGroup.Query().Where(apikeygroup.ProjectIDEQ(target.ID), apikeygroup.NameEQ(source.Name)).Only(ctx)
		if ent.IsNotFound(err) || (err == nil && strategy == ConflictStrategyOverwrite) {
			if err := objects.ValidateGlobalRate(source.GlobalRate); err != nil {
				return fmt.Errorf("restore API key group %q: %w", source.Name, err)
			}
		}
		if ent.IsNotFound(err) {
			existing, err = db.APIKeyGroup.Create().SetProjectID(target.ID).SetName(source.Name).SetNote(source.Note).SetEnabled(source.Enabled).
				SetNillableGlobalRate(source.GlobalRate).SetShowChannelInSelfUsage(source.ShowChannelInSelfUsage).Save(ctx)
		} else if err == nil {
			switch strategy {
			case ConflictStrategySkip, "":
				continue
			case ConflictStrategyError:
				return fmt.Errorf("API key group %s already exists", source.Name)
			case ConflictStrategyOverwrite:
				update := existing.Update().SetNote(source.Note).SetEnabled(source.Enabled).
					SetShowChannelInSelfUsage(source.ShowChannelInSelfUsage).AddRevision(1)
				if source.GlobalRate == nil {
					update.ClearGlobalRate()
				} else {
					update.SetGlobalRate(*source.GlobalRate)
				}
				err = update.Exec(ctx)
			}
		}
		if err != nil {
			return err
		}
		if source.ChannelRates != nil {
			if err = restoreChannelRates(ctx, db, channels, nil, &existing.ID, source.ChannelRates); err != nil {
				return err
			}
		}
	}
	return nil
}

func restoreChannelRates(ctx context.Context, db *ent.Client, channels *channelRestoreState, keyID, groupID *int, rates []BackupChannelRate) error {
	if rates == nil {
		return nil
	}
	if keyID != nil {
		if _, err := db.APIKeyChannelRate.Delete().Where(apikeychannelrate.KeyIDEQ(*keyID)).Exec(ctx); err != nil {
			return err
		}
	} else if _, err := db.APIKeyGroupChannelRate.Delete().Where(apikeygroupchannelrate.GroupIDEQ(*groupID)).Exec(ctx); err != nil {
		return err
	}
	for _, r := range rates {
		if r.Multiplier.IsNegative() {
			return fmt.Errorf("invalid channel rate")
		}
		id, err := channels.resolve(r.ChannelID, r.ChannelName)
		if err != nil {
			return err
		}
		if id == 0 {
			return fmt.Errorf("missing channel for restored rate: %d", r.ChannelID)
		}
		if keyID != nil {
			err = db.APIKeyChannelRate.Create().SetKeyID(*keyID).SetChannelID(id).SetMultiplier(r.Multiplier).Exec(ctx)
		} else {
			err = db.APIKeyGroupChannelRate.Create().SetGroupID(*groupID).SetChannelID(id).SetMultiplier(r.Multiplier).Exec(ctx)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
