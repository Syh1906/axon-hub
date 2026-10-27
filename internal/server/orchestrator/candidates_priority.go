package orchestrator

import (
	"context"
	"slices"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

type ChannelTagPrioritySelector struct {
	wrapped CandidateSelector
	tags    []string
}

func WithChannelTagPrioritySelector(wrapped CandidateSelector, tags []string) *ChannelTagPrioritySelector {
	return &ChannelTagPrioritySelector{wrapped: wrapped, tags: slices.Clone(tags)}
}

func (s *ChannelTagPrioritySelector) Select(ctx context.Context, req *llm.Request) ([]*ChannelModelsCandidate, error) {
	candidates, err := s.wrapped.Select(ctx, req)
	if err != nil {
		return nil, err
	}
	priorities := make([]objects.ChannelPriority, 0, len(candidates))
	for _, candidate := range candidates {
		if priority := candidate.Channel.Settings.PriorityForTags(candidate.Channel.Tags, s.tags); priority != nil {
			priorities = append(priorities, objects.ChannelPriority{ChannelID: candidate.Channel.ID, Priority: *priority})
		}
	}
	return applyChannelPriorities(candidates, priorities), nil
}

// A key's tag selection takes precedence over its project's tags. Exclusion
// selections restrict access but never supply inherited priority values.
func channelPriorityTags(key *ent.APIKey) []string {
	if key == nil {
		return nil
	}
	if profile := key.GetActiveProfile(); profile != nil && len(profile.ChannelTags) > 0 {
		if profile.ChannelTagsMatchMode == objects.ChannelTagsMatchModeNone {
			return nil
		}
		return profile.ChannelTags
	}
	if project := key.Edges.Project; project != nil {
		if profile := project.GetActiveProfile(); profile != nil && profile.ChannelTagsMatchMode != objects.ChannelTagsMatchModeNone {
			return profile.ChannelTags
		}
	}
	return nil
}

type ChannelPrioritySelector struct {
	wrapped    CandidateSelector
	priorities []objects.ChannelPriority
}

func WithChannelPrioritySelector(wrapped CandidateSelector, priorities []objects.ChannelPriority) *ChannelPrioritySelector {
	return &ChannelPrioritySelector{wrapped: wrapped, priorities: priorities}
}

func (s *ChannelPrioritySelector) Select(ctx context.Context, req *llm.Request) ([]*ChannelModelsCandidate, error) {
	candidates, err := s.wrapped.Select(ctx, req)
	if err != nil {
		return nil, err
	}
	return applyChannelPriorities(candidates, s.priorities), nil
}

// The wrapped selector has already resolved per-model endpoint formats. Merge
// those formats with the model entries so retries retain their selected protocol.
// Never mutate the shared channel or another request's candidate slices.
func applyChannelPriorities(candidates []*ChannelModelsCandidate, priorities []objects.ChannelPriority) []*ChannelModelsCandidate {
	if len(priorities) == 0 {
		return candidates
	}
	overrides := make(map[int]int, len(priorities))
	for _, entry := range priorities {
		overrides[entry.ChannelID] = entry.Priority
	}
	type key struct{ channelID, priority int }
	positions := make(map[key]int)
	seenModels := make(map[key]map[string]bool)
	result := make([]*ChannelModelsCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		priority := candidate.Priority
		if override, ok := overrides[candidate.Channel.ID]; ok {
			priority = override
		}
		identity := key{candidate.Channel.ID, priority}
		position, exists := positions[identity]
		if !exists {
			copy := *candidate
			copy.Priority = priority
			copy.Models = slices.Clone(candidate.Models)
			copy.modelAPIFormats = slices.Clone(candidate.modelAPIFormats)
			position = len(result)
			positions[identity] = position
			result = append(result, &copy)
			seenModels[identity] = make(map[string]bool, len(copy.Models))
			for _, entry := range copy.Models {
				seenModels[identity][entry.ActualModel] = true
			}
			continue
		}
		target := result[position]
		haveFormats := len(target.modelAPIFormats) == len(target.Models) && len(candidate.modelAPIFormats) == len(candidate.Models)
		for index, entry := range candidate.Models {
			if !seenModels[identity][entry.ActualModel] {
				target.Models = append(target.Models, entry)
				if haveFormats {
					target.modelAPIFormats = append(target.modelAPIFormats, candidate.modelAPIFormats[index])
				} else {
					target.modelAPIFormats = nil
				}
				seenModels[identity][entry.ActualModel] = true
			}
		}
	}
	return result
}
