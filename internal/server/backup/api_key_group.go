package backup

import (
	"context"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
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
