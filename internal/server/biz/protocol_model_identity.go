package biz

import (
	"slices"
	"strings"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/model"
)

type ProtocolModelIdentity struct {
	EntityID int
	ModelID  string
	Name     string
}

// ProtocolModelIdentities uses the same exact/rule precedence as model discovery.
// Only identity columns are needed; callers load associations for the page only.
func ProtocolModelIdentities(models []*ent.Model, search string) []ProtocolModelIdentity {
	resolver := newModelResolver(models)
	seen := make(map[string]bool)
	result := make([]ProtocolModelIdentity, 0, len(models))
	search = strings.ToLower(strings.TrimSpace(search))
	for _, candidate := range models {
		for _, id := range PublicModelIDs(candidate) {
			if seen[id] {
				continue
			}
			seen[id] = true
			resolved := resolver.resolve(id)
			if resolved == nil || resolved.Type != model.TypeChat || !slices.Contains(PublicModelIDs(resolved), id) {
				continue
			}
			if search != "" && !strings.Contains(strings.ToLower(id), search) && !strings.Contains(strings.ToLower(resolved.Name), search) {
				continue
			}
			result = append(result, ProtocolModelIdentity{EntityID: resolved.ID, ModelID: id, Name: resolved.Name})
		}
	}
	slices.SortFunc(result, func(a, b ProtocolModelIdentity) int { return strings.Compare(a.ModelID, b.ModelID) })
	return result
}
