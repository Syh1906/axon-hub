package biz

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"sync"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
)

// ResolveModel keeps exact model lookups indexed. Other enabled rules are
// evaluated in priority/ID order, so overlaps are deterministic, not errors.
func (svc *ModelService) ResolveModel(ctx context.Context, requestID string) (*ent.Model, error) {
	for matched, err := range svc.MatchingModels(ctx, requestID) {
		return matched, err
	}
	return nil, &ent.NotFoundError{}
}

func (svc *ModelService) MatchingModels(ctx context.Context, requestID string) iter.Seq2[*ent.Model, error] {
	return func(yield func(*ent.Model, error) bool) {
		exact, err := svc.GetModelByModelID(ctx, requestID, model.StatusEnabled)
		switch {
		case err == nil:
			if !yield(exact, nil) {
				return
			}
		case !ent.IsNotFound(err):
			yield(nil, err)
			return
		}
		rules, err := svc.entFromContext(ctx).Model.Query().Where(
			model.StatusEQ(model.StatusEnabled),
			model.MatchTypeNEQ(objects.ModelMatchExact),
		).Order(model.ByMatchPriority(), model.ByID()).All(ctx)
		if err != nil {
			yield(nil, err)
			return
		}
		for rule := range matchingModelRules(rules, requestID) {
			if !yield(rule, nil) {
				return
			}
		}
	}
}

func matchingModelRules(rules []*ent.Model, requestID string) iter.Seq[*ent.Model] {
	return func(yield func(*ent.Model) bool) {
		for _, rule := range rules {
			if matched, _ := objects.MatchModelID(rule.MatchType, rule.ModelID, requestID); matched && !yield(rule) {
				return
			}
		}
	}
}

type ModelChannelScope struct {
	allows  func(*ent.Channel) bool
	targets func() ([]*ent.Channel, error)
}

func (svc *ModelService) ModelChannelScope(ctx context.Context, key *ent.APIKey) *ModelChannelScope {
	if key == nil {
		return nil
	}
	return svc.modelChannelScope(ctx, key.Edges.Project.GetActiveProfile(), key.GetActiveProfile())
}

func (svc *ModelService) modelChannelScope(ctx context.Context, project *objects.ProjectProfile, profile *objects.APIKeyProfile) *ModelChannelScope {
	if (project == nil || len(project.ChannelIDs)+len(project.ChannelTags) == 0) &&
		(profile == nil || len(profile.ChannelIDs)+len(profile.ChannelTags) == 0) {
		return nil
	}
	return &ModelChannelScope{
		allows: func(ch *ent.Channel) bool {
			return (project == nil || (len(project.ChannelIDs) == 0 || slices.Contains(project.ChannelIDs, ch.ID)) && project.MatchChannelTags(ch.Tags)) &&
				(profile == nil || (len(profile.ChannelIDs) == 0 || slices.Contains(profile.ChannelIDs, ch.ID)) && profile.MatchChannelTags(ch.Tags))
		},
		targets: sync.OnceValues(func() ([]*ent.Channel, error) {
			client := svc.entFromContext(ctx)
			return authz.RunAsHost(ctx, client, "model-association-channel-scope", func(hostCtx context.Context) ([]*ent.Channel, error) {
				return client.Channel.Query().Where(channel.StatusIn(channel.StatusEnabled, channel.StatusDisabled)).
					Select(channel.FieldID, channel.FieldName, channel.FieldTags).All(hostCtx)
			})
		}),
	}
}

func (scope *ModelChannelScope) Allows(ch *ent.Channel) bool {
	return scope.allows(ch)
}

func (scope *ModelChannelScope) ExcludesAllTargets(associations []*objects.ModelAssociation) (bool, error) {
	if !lo.ContainsBy(associations, func(assoc *objects.ModelAssociation) bool { return !assoc.Disabled }) {
		return false, nil
	}
	channels, err := scope.targets()
	if err != nil {
		return false, fmt.Errorf("query model association channel targets: %w", err)
	}
	hasTarget := false
	for _, ch := range channels {
		if lo.ContainsBy(associations, func(assoc *objects.ModelAssociation) bool {
			return !assoc.Disabled && associationTargetsChannel(assoc, ch)
		}) {
			if scope.Allows(ch) {
				return false, nil
			}
			hasTarget = true
		}
	}
	return hasTarget, nil
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
	for matched := range r.matching(requestID) {
		return matched
	}
	return nil
}

func (r modelResolver) matching(requestID string) iter.Seq[*ent.Model] {
	return func(yield func(*ent.Model) bool) {
		if exact := r.exact[requestID]; exact != nil && !yield(exact) {
			return
		}
		matchingModelRules(r.rules, requestID)(yield)
	}
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
