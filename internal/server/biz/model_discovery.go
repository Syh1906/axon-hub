package biz

import "github.com/looplj/axonhub/internal/objects"

// DiscoveryMatches retains each association until the client's protocol is
// known. The visibility list deduplicates request names across associations,
// even when a mapped name and a pass-through name reach different actual models.
func (m ModelFacade) DiscoveryMatches() []*AssociationMatch {
	if len(m.associations) > 1 {
		return MatchAssociationsForModel(m.associations, m.discoveryChannels, m.TargetID)
	}
	association := &objects.ModelAssociation{}
	if len(m.associations) == 1 {
		association = m.associations[0]
	}
	return []*AssociationMatch{{Association: association, Connections: m.Connections}}
}
