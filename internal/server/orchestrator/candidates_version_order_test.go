package orchestrator

import (
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRegexModelStaticAssociationCache(t *testing.T) {
	ctx, client := setupTest(t)
	channels := createTestChannels(t, ctx, client)
	selector := NewDefaultSelector(newTestChannelServiceForChannels(client), newTestModelService(client), newTestSystemService(client))
	model := &ent.Model{ModelID: "client-.*", MatchType: objects.ModelMatchRegex}
	assoc := &objects.ModelAssociation{Type: "regex", Regex: &objects.RegexAssociation{Pattern: `(?<version>.*)`}}
	rules := []*objects.ModelAssociation{assoc}
	_, err := selector.resolveAssociationsForRequest(ctx, model, rules, "client-a")
	require.NoError(t, err)
	entry := selector.associationCache["regex:client-.*"]
	require.NotNil(t, entry)
	_, err = selector.resolveAssociationsForRequest(ctx, model, rules, "client-b")
	require.NoError(t, err)
	require.Same(t, entry, selector.associationCache["regex:client-.*"])
	assoc.PreferLatestVersion = true
	_, err = selector.resolveAssociationsForRequest(ctx, model, rules, "client-b")
	require.NoError(t, err)
	require.NotSame(t, entry, selector.associationCache["regex:client-.*"])
	dynamic := []*objects.ModelAssociation{{Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: channels[0].ID}}}
	for _, id := range []string{"client-a", "client-b"} {
		result, err := selector.resolveAssociationsForRequest(ctx, model, dynamic, id)
		require.NoError(t, err)
		require.Equal(t, id, result[0].models[0].ActualModel)
	}
}

func TestModelVersionSettingsRoundTrip(t *testing.T) {
	ctx, client := setupTest(t)
	settings := &objects.ModelSettings{Associations: []*objects.ModelAssociation{{Type: "regex", PreferLatestVersion: true, Regex: &objects.RegexAssociation{Pattern: `^gpt-(?<version>[0-9]+(?:[.][0-9]+)*)-sol$`}}}}
	saved := client.Model.Create().SetModelID("version-roundtrip").SetName("Version test").SetIcon("test").SetGroup("test").SetDeveloper("test").SetModelCard(&objects.ModelCard{}).SetSettings(settings).SaveX(ctx)
	read := client.Model.GetX(ctx, saved.ID)
	require.True(t, read.Settings.Associations[0].PreferLatestVersion)
	read.Settings.Associations[0].PreferLatestVersion = false
	client.Model.UpdateOneID(saved.ID).SetSettings(read.Settings).SaveX(ctx)
	require.False(t, client.Model.GetX(ctx, saved.ID).Settings.Associations[0].PreferLatestVersion)
}
