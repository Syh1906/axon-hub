package biz

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

func TestAssociationPricePreviewOverlappingRoutes(t *testing.T) {
	channel := &ent.Channel{ID: 1}
	entry := func(alias, actual, source string, card int) ChannelModelEntry {
		return ChannelModelEntry{RequestModel: alias, ActualModel: actual, PreviewPriceSource: source, PreviewPriceModel: "display", previewPriceCardID: card}
	}
	luna := entry("luna", "luna", "standard", 1)
	terra := entry("luna", "terra", "standard", 2)
	target := entry("luna", "terra", "standard", 3)
	channelPrice := entry("luna", "terra", "channel", 0)
	unpriced := entry("luna", "terra", "unpriced", 0)
	scoped := func(e ChannelModelEntry, conditional bool) *AssociationMatch {
		return &AssociationMatch{Association: &objects.ModelAssociation{When: &objects.ModelAssociationWhen{Enabled: conditional, Condition: &objects.Condition{}}},
			Connections: []*ModelChannelConnection{{Channel: channel, Models: []ChannelModelEntry{e}}}}
	}
	alias := target
	alias.RequestModel = "other-alias"
	for _, tc := range []struct {
		name          string
		first, second ChannelModelEntry
		conditional   bool
		want          []string
	}{
		{"same card display names", terra, target, true, []string{"conditional"}},
		{"channel price takes precedence for both", channelPrice, channelPrice, true, []string{"channel"}},
		{"conditionally missing", unpriced, target, true, []string{"conditionalUnpriced"}},
		{"first unconditional always wins", terra, target, false, []string{"standard"}},
		{"same alias, different outbound model", luna, target, false, []string{"standard", "standard"}},
		{"different alias, same outbound model", terra, alias, false, []string{"conditional", "conditional"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connections := mergeAssociationPricePreviews([]*AssociationMatch{scoped(tc.first, tc.conditional), scoped(tc.second, false)})
			var sources []string
			for _, conn := range connections {
				for _, model := range conn.Models {
					sources = append(sources, model.PreviewPriceSource)
				}
			}
			require.Equal(t, tc.want, sources)
		})
	}
}
