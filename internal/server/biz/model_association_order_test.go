package biz

import (
	"fmt"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAssociationVersionOrder(t *testing.T) {
	pattern := `^gpt-(?<version>[0-9]+(?:[.][0-9]+)*)-sol$`
	ch := &Channel{Channel: &ent.Channel{ID: 1, Tags: []string{"Luna"}, SupportedModels: []string{"gpt-6.9-sol", "gpt-6.10-sol", "gpt-6.1-sol", "gpt-6.2-terra"}}}
	for _, kind := range []string{"regex", "channel_regex", "channel_tags_regex"} {
		t.Run(kind, func(t *testing.T) {
			assoc := &objects.ModelAssociation{Type: kind, Priority: 6, PreferLatestVersion: true, Regex: &objects.RegexAssociation{Pattern: pattern}, ChannelRegex: &objects.ChannelRegexAssociation{ChannelID: 1, Pattern: pattern}, ChannelTagsRegex: &objects.ChannelTagsRegexAssociation{ChannelTags: []string{"Luna"}, Pattern: pattern}}
			require.NoError(t, validateAssociationVersionOrder(assoc))
			for range 10 {
				got := MatchConnections([]*objects.ModelAssociation{assoc}, []*Channel{ch})
				require.Len(t, got, 1)
				require.Equal(t, 6, got[0].Priority)
				require.True(t, got[0].PreferLatestVersion)
				require.Equal(t, []string{"gpt-6.10-sol", "gpt-6.9-sol", "gpt-6.1-sol"}, []string{got[0].Models[0].ActualModel, got[0].Models[1].ActualModel, got[0].Models[2].ActualModel})
			}
			assoc.PreferLatestVersion = false
			require.Len(t, MatchConnections([]*objects.ModelAssociation{assoc}, []*Channel{ch})[0].Models, 3)
		})
	}
	require.Equal(t, 0, compareModelVersions(numericModelVersion("06.1"), numericModelVersion("6.1.0")))
	require.Greater(t, compareModelVersions(numericModelVersion("999999999999999999999"), numericModelVersion("9")), 0)
	for _, value := range []string{"", "latest", "1..2", "1-rc1"} {
		require.Nil(t, numericModelVersion(value))
	}
	require.Error(t, validateAssociationVersionOrder(&objects.ModelAssociation{Type: "regex", PreferLatestVersion: true, Regex: &objects.RegexAssociation{Pattern: "gpt-.*"}}))
	require.NoError(t, validateAssociationVersionOrder(&objects.ModelAssociation{Type: "regex", Regex: &objects.RegexAssociation{Pattern: "gpt-.*"}}))
}

func BenchmarkAssociationVersionOrder(b *testing.B) {
	ch := &Channel{Channel: &ent.Channel{ID: 1}}
	for i := 0; i < 100; i++ {
		ch.SupportedModels = append(ch.SupportedModels, fmt.Sprintf("gpt-6.%d-sol", i))
	}
	entries := ch.GetModelEntries()
	assoc := &objects.ModelAssociation{PreferLatestVersion: true}
	b.ReportAllocs()
	for b.Loop() {
		matchVersionedEntries(assoc, `^gpt-(?<version>[0-9]+(?:[.][0-9]+)*)-sol$`, entries, NewDuplicateKeyTracker(), 1)
	}
}

func TestVersionPreviewWarnsWithoutBlockingValidModels(t *testing.T) {
	assoc := &objects.ModelAssociation{Type: "regex", PreferLatestVersion: true, Regex: &objects.RegexAssociation{Pattern: `^gpt-(?<version>.+)-sol$`}}
	channels := []*Channel{{Channel: &ent.Channel{ID: 1, SupportedModels: []string{"gpt-latest-sol", "gpt-6.2-sol"}}}}
	preview := appendVersionPreviewWarnings(MatchConnections([]*objects.ModelAssociation{assoc}, channels), []*objects.ModelAssociation{assoc}, channels)
	require.Len(t, preview, 1)
	require.Equal(t, []string{"gpt-latest-sol"}, preview[0].UnrankedModelIDs)
	require.Equal(t, "gpt-6.2-sol", preview[0].Models[0].ActualModel)
	matches := MatchConnections([]*objects.ModelAssociation{assoc}, channels)
	require.Len(t, matches[0].Models, 1)
	require.Equal(t, "gpt-6.2-sol", matches[0].Models[0].ActualModel)
}

func TestVersionOrderKeepsRuleAndChannelBoundaries(t *testing.T) {
	channels := []*Channel{
		{Channel: &ent.Channel{ID: 1, SupportedModels: []string{"gpt-6.1-sol", "gpt-06.1-sol", "gpt-6.1-terra", "gpt-latest-sol"}}},
		{Channel: &ent.Channel{ID: 2, SupportedModels: []string{"gpt-6.10-sol", "gpt-6.2-sol"}}},
	}
	rules := []*objects.ModelAssociation{
		{Type: "regex", Priority: 5, Regex: &objects.RegexAssociation{Pattern: `^gpt-6.1-terra$`}},
		{Type: "regex", Priority: 6, PreferLatestVersion: true, Regex: &objects.RegexAssociation{Pattern: `^gpt-(?<version>.+)-sol$`}},
	}
	for range 10 {
		matches := MatchConnections(rules, channels)
		preview := appendVersionPreviewWarnings(matches, rules, channels)
		require.Len(t, preview, 3)
		require.Equal(t, 5, preview[0].Priority)
		require.Equal(t, "gpt-6.1-terra", preview[0].Models[0].RequestModel)
		require.Equal(t, 1, preview[1].Channel.ID)
		require.Equal(t, 6, preview[1].Priority)
		require.Equal(t, "gpt-06.1-sol", preview[1].Models[0].RequestModel)
		require.Equal(t, "gpt-6.1-sol", preview[1].Models[1].RequestModel)
		require.Equal(t, []string{"gpt-latest-sol"}, preview[1].UnrankedModelIDs)
		require.Equal(t, 2, preview[2].Channel.ID)
		require.Equal(t, "gpt-6.10-sol", preview[2].Models[0].RequestModel)
		require.Equal(t, "gpt-6.2-sol", preview[2].Models[1].RequestModel)
	}
	onlyWarning := appendVersionPreviewWarnings(nil, rules, []*Channel{{Channel: &ent.Channel{ID: 3, SupportedModels: []string{"gpt-latest-sol"}}}})
	require.Len(t, onlyWarning, 1)
	require.Empty(t, onlyWarning[0].Models)
	require.Equal(t, []string{"gpt-latest-sol"}, onlyWarning[0].UnrankedModelIDs)
}
