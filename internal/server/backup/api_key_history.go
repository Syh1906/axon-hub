package backup

import (
	"context"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/schema/schematype"
	"github.com/looplj/axonhub/internal/server/biz"
)

// Include irreversible deletion records, never legacy soft-deleted secrets.
// Callers execute this query with SkipSoftDelete while retaining privacy rules.
func backupAPIKeys(db *ent.Client) *ent.APIKeyQuery {
	return db.APIKey.Query().Where(apikey.Or(apikey.DeletedAtEQ(0), apikey.KeyHasPrefix(biz.DeletedAPIKeyPrefix)))
}

func attachBackupAPIKeys(ctx context.Context, db *ent.Client, requests []*ent.Request) error {
	ids := make([]int, 0, len(requests))
	for _, request := range requests {
		if request.APIKeyID > 0 {
			ids = append(ids, request.APIKeyID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	keys, err := backupAPIKeys(db).Where(apikey.IDIn(ids...)).Select(apikey.FieldID, apikey.FieldKey).All(schematype.SkipSoftDelete(ctx))
	if err != nil {
		return err
	}
	byID := make(map[int]*ent.APIKey, len(keys))
	for _, key := range keys {
		byID[key.ID] = key
	}
	for _, request := range requests {
		request.Edges.APIKey = byID[request.APIKeyID]
	}
	return nil
}
