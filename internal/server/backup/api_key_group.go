package backup

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
	"github.com/looplj/axonhub/internal/ent/project"
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
			groups, err := svc.db.APIKeyGroup.Query().Where(apikeygroup.IDGT(last)).WithProject().Order(ent.Asc(apikeygroup.FieldID)).Limit(backupBatchSize).All(ctx)
			if len(groups) > 0 {
				last = groups[len(groups)-1].ID
			}
			return groups, last, err
		}, func(group *ent.APIKeyGroup) ([]byte, bool, error) {
			projectName := ""
			if group.Edges.Project != nil {
				projectName = group.Edges.Project.Name
			}
			data, err := json.Marshal(BackupAPIKeyGroup{ProjectName: projectName, Name: group.Name, Note: group.Note, Enabled: group.Enabled})
			return data, true, err
		})
}

func (svc *BackupService) restoreAPIKeyGroups(ctx context.Context, db *ent.Client, groups []*BackupAPIKeyGroup, strategy ConflictStrategy) error {
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
		if ent.IsNotFound(err) {
			_, err = db.APIKeyGroup.Create().SetProjectID(target.ID).SetName(source.Name).SetNote(source.Note).SetEnabled(source.Enabled).Save(ctx)
		} else if err == nil {
			switch strategy {
			case ConflictStrategyError:
				return fmt.Errorf("API key group %s already exists", source.Name)
			case ConflictStrategyOverwrite:
				_, err = existing.Update().SetNote(source.Note).SetEnabled(source.Enabled).AddRevision(1).Save(ctx)
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}
