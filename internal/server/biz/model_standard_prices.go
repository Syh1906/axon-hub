package biz

import (
	"context"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
)

// ModelStandardPriceMatch keeps the requested channel model separate from the
// catalog rule that supplies its standard price.
type ModelStandardPriceMatch struct {
	RequestModelID string
	Model          *ent.Model
}

func (svc *ModelService) MatchModelStandardPrices(ctx context.Context, modelIDs []string) ([]*ModelStandardPriceMatch, error) {
	return matchModelStandardPrices(ctx, svc.entFromContext(ctx), modelIDs)
}

// Management price matching includes disabled catalog entries, as automatic
// channel price initialization already does. Runtime routing remains enabled-only.
func matchModelStandardPrices(ctx context.Context, db *ent.Client, modelIDs []string) ([]*ModelStandardPriceMatch, error) {
	modelIDs = uniqueNonEmptyModelIDs(modelIDs)
	result := make([]*ModelStandardPriceMatch, 0, len(modelIDs))
	if len(modelIDs) == 0 {
		return result, nil
	}
	catalog, err := db.Model.Query().Where(
		model.MatchTypeNEQ(objects.ModelMatchExact),
		model.StatusIn(model.StatusEnabled, model.StatusDisabled),
	).All(ctx)
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(modelIDs); start += channelModelPriceQueryBatchSize {
		end := min(start+channelModelPriceQueryBatchSize, len(modelIDs))
		exact, err := db.Model.Query().Where(
			model.ModelIDIn(modelIDs[start:end]...),
			model.MatchTypeEQ(objects.ModelMatchExact),
			model.StatusIn(model.StatusEnabled, model.StatusDisabled),
		).All(ctx)
		if err != nil {
			return nil, err
		}
		catalog = append(catalog, exact...)
	}
	resolver := newModelResolver(catalog)
	for _, id := range modelIDs {
		if matched := resolver.resolve(id); matched != nil {
			result = append(result, &ModelStandardPriceMatch{RequestModelID: id, Model: matched})
		}
	}
	return result, nil
}
