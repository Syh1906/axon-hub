package biz

import (
	"context"
	"fmt"
	"slices"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
)

// ResolveModel keeps exact model lookups indexed. Other enabled rules are
// evaluated in priority/ID order, so overlaps are deterministic, not errors.
func (svc *ModelService) ResolveModel(ctx context.Context, requestID string) (*ent.Model, error) {
	exact, err := svc.GetModelByModelID(ctx, requestID, model.StatusEnabled)
	if err == nil || !ent.IsNotFound(err) {
		return exact, err
	}
	rules, err := svc.entFromContext(ctx).Model.Query().Where(
		model.StatusEQ(model.StatusEnabled),
		model.MatchTypeNEQ(objects.ModelMatchExact),
	).Order(model.ByMatchPriority(), model.ByID()).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, rule := range rules {
		if matches, _ := objects.MatchModelID(rule.MatchType, rule.ModelID, requestID); matches {
			return rule, nil
		}
	}
	return nil, &ent.NotFoundError{}
}

func validateModelIdentity(kind objects.ModelMatchType, pattern string, publicIDs []string) error {
	if err := objects.ValidateModelMatch(kind, pattern); err != nil {
		return err
	}
	for _, publicID := range publicIDs {
		matched, err := objects.MatchModelID(kind, pattern, publicID)
		if err != nil {
			return err
		}
		if publicID == "" || !matched {
			return fmt.Errorf("public model ID %q does not match the model rule", publicID)
		}
	}
	return nil
}

func modelMatchType(kind *objects.ModelMatchType) objects.ModelMatchType {
	if kind == nil {
		return objects.ModelMatchExact
	}
	return *kind
}

// PublicModelIDs distinguishes legacy null from an explicit empty list. New
// rule models never expose their expression as a discoverable model ID.
func PublicModelIDs(m *ent.Model) []string {
	if m.PublicModelIds != nil {
		return m.PublicModelIds
	}
	if m.MatchType == "" || m.MatchType == objects.ModelMatchExact {
		return []string{m.ModelID}
	}
	return nil
}

// modelResolver resolves many discovery IDs without querying per public ID.
type modelResolver struct {
	exact map[string]*ent.Model
	rules []*ent.Model
}

func newModelResolver(models []*ent.Model) modelResolver {
	r := modelResolver{exact: make(map[string]*ent.Model)}
	for _, m := range models {
		if m.MatchType == "" || m.MatchType == objects.ModelMatchExact {
			r.exact[m.ModelID] = m
		} else {
			r.rules = append(r.rules, m)
		}
	}
	slices.SortFunc(r.rules, func(a, b *ent.Model) int {
		if a.MatchPriority < b.MatchPriority {
			return -1
		}
		if a.MatchPriority > b.MatchPriority {
			return 1
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return r
}

func (r modelResolver) resolve(requestID string) *ent.Model {
	if exact := r.exact[requestID]; exact != nil {
		return exact
	}
	for _, rule := range r.rules {
		if matched, _ := objects.MatchModelID(rule.MatchType, rule.ModelID, requestID); matched {
			return rule
		}
	}
	return nil
}

func normalizePublicModelIDs(ids []string) []string {
	if ids == nil {
		return nil
	}
	return lo.Uniq(ids)
}

// Catalog associations are used only by the admin coverage view. A dynamic
// rule covers catalog IDs matching its entry rule, not every ID in a channel.
func modelCatalogAssociations(settings *SystemModelSettings, m *ent.Model, channels []*ent.Channel) []*objects.ModelAssociation {
	result := EffectiveModelAssociations(settings, m)
	seen := make(map[string]bool)
	for _, ch := range channels {
		for id := range (&Channel{Channel: ch}).GetModelEntries() {
			if seen[id] {
				continue
			}
			seen[id] = true
			matched, _ := objects.MatchModelID(m.MatchType, m.ModelID, id)
			if !matched {
				continue
			}
			for _, assoc := range EffectiveModelAssociationsForRequest(settings, m, id) {
				if assoc.Type == "channel_request" && assoc.ChannelRequest != nil {
					assoc = cloneModelAssociation(assoc)
					assoc.Type = "channel_model"
					assoc.ChannelModel = &objects.ChannelModelAssociation{ChannelID: assoc.ChannelRequest.ChannelID, ModelID: id}
					assoc.ChannelRequest = nil
				}
				result = append(result, assoc)
			}
		}
	}
	return result
}
