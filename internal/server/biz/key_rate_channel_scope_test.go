package biz

import (
	"context"
	"testing"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/stretchr/testify/require"
)

func TestRateChannelScope(t *testing.T) {
	ctx, db, models, channels, _ := setupListEnabledModelsTest(t)
	create := func(tag string, status channel.Status) *ent.Channel {
		return db.Channel.Create().SetName("same").SetTags([]string{tag}).SetType(channel.TypeOpenai).
			SetBaseURL("http://127.0.0.1:1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture-only"}).SetSupportedModels([]string{"upstream"}).
			SetDefaultTestModel("upstream").SetStatus(status).SaveX(ctx)
	}
	direct := create("direct", channel.StatusEnabled)
	relay := create("relay", channel.StatusEnabled)
	create("blocked", channel.StatusEnabled)
	create("direct", channel.StatusDisabled)
	reloadEnabledChannels(t, ctx, db, channels)
	createConfiguredModel(t, ctx, db, "public", model.TypeChat, &objects.ModelAssociation{
		Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: direct.ID, ModelID: "upstream"},
	})
	key := &ent.APIKey{
		Edges: ent.APIKeyEdges{Project: &ent.Project{Profiles: &objects.ProjectProfiles{
			ActiveProfile: "project", Profiles: []objects.ProjectProfile{{Name: "project", ChannelTags: []string{"blocked"}, ChannelTagsMatchMode: objects.ChannelTagsMatchModeNone}},
		}}},
		Profiles: &objects.APIKeyProfiles{ActiveProfile: "daily", Profiles: []objects.APIKeyProfile{
			{Name: "daily", ModelIDs: []string{"alias"}, ModelMappings: []objects.ModelMapping{{From: "alias", To: "public"}}},
			{Name: "backup", ChannelIDs: []int{direct.ID, relay.ID}, ChannelTags: []string{"direct", "relay"}},
			{Name: "empty", ChannelTags: []string{"missing"}},
		}},
	}
	scope, err := models.rateChannelScope(ctx, key)
	require.NoError(t, err)
	require.Equal(t, "daily", scope.ActiveProfile)
	require.Equal(t, []string{"daily", "backup", "empty"}, scope.Profiles)
	require.Equal(t, []*objects.KeyRateChannel{
		{ChannelID: direct.ID, Name: "same", Tags: []string{"direct"}, Profiles: []string{"daily", "backup"}},
		{ChannelID: relay.ID, Name: "same", Tags: []string{"relay"}, Profiles: []string{"backup"}},
	}, scope.Channels)

	key.Profiles = nil
	scope, err = models.rateChannelScope(ctx, key)
	require.NoError(t, err)
	require.Equal(t, []string{""}, scope.Profiles)
	require.Len(t, scope.Channels, 2)
}

func TestChannelRateProjectOwnerPermissions(t *testing.T) {
	seed, db, models, channels, _ := setupListEnabledModelsTest(t)
	svc := &APIKeyService{AbstractService: &AbstractService{db: db}}
	project := db.Project.Create().SetName("project").SaveX(seed)
	actor := db.User.Create().SetEmail("project-owner@example.test").SetPassword("fixture").SaveX(seed)
	db.UserProject.Create().SetProjectID(project.ID).SetUserID(actor.ID).SetIsOwner(true).SaveX(seed)
	key := db.APIKey.Create().SetName("sample").SetKey("fixture").SetProjectID(project.ID).SetUserID(actor.ID).SaveX(seed)
	group := db.APIKeyGroup.Create().SetName("group").SetProjectID(project.ID).SaveX(seed)
	ch := db.Channel.Create().SetName("sample").SetTags([]string{"sample-tag"}).SetType(channel.TypeOpenai).
		SetBaseURL("http://127.0.0.1:1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture-only"}).
		SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SetStatus(channel.StatusEnabled).SaveX(seed)
	reloadEnabledChannels(t, seed, db, channels)
	asActor := func() context.Context {
		u := db.User.Query().WithProjectUsers().WithRoles().OnlyX(seed)
		ctx := contexts.WithProjectID(contexts.WithUser(ent.NewContext(context.Background(), db), u), project.ID)
		return authz.NewUserContext(ctx, u.ID)
	}

	ctx := asActor()
	rows, err := svc.KeyRateChannelScope(ctx, models, key.ID)
	require.ErrorContains(t, err, "read_channels")
	require.Nil(t, rows)
	_, err = svc.KeyChannelRates(ctx, nil, &group.ID)
	require.ErrorContains(t, err, "read_channels")
	_, err = svc.costReportProject(ctx, group.ID)
	require.ErrorContains(t, err, "read_channels")
	states, err := channels.ChannelUpstreamStates(ctx, []int{ch.ID})
	require.Error(t, err)
	require.Nil(t, states)

	// 渠道读取权限允许查看倍率；保存还需渠道写入权限。
	db.User.UpdateOneID(actor.ID).SetScopes([]string{string(scopes.ScopeReadChannels)}).ExecX(seed)
	ctx = asActor()
	rows, err = svc.KeyRateChannelScope(ctx, models, key.ID)
	require.NoError(t, err)
	require.Len(t, rows.Channels, 1)
	require.Equal(t, ch.ID, rows.Channels[0].ChannelID)
	states, err = channels.ChannelUpstreamStates(ctx, []int{ch.ID})
	require.NoError(t, err)
	require.Len(t, states, 1)
	_, err = svc.costReportProject(ctx, group.ID)
	require.NoError(t, err)
	rates, err := svc.KeyChannelRates(ctx, &key.ID, nil)
	require.NoError(t, err)
	require.ErrorContains(t, svc.SaveKeyChannelRates(ctx, &key.ID, nil, rates.Revision, nil), "write_channels")

	db.User.UpdateOneID(actor.ID).SetScopes([]string{string(scopes.ScopeReadChannels), string(scopes.ScopeWriteChannels)}).ExecX(seed)
	ctx = asActor()
	require.NoError(t, svc.SaveKeyChannelRates(ctx, &key.ID, nil, rates.Revision, nil))
	other := db.Project.Create().SetName("other").SaveX(seed)
	_, err = svc.KeyRateChannelScope(contexts.WithProjectID(ctx, other.ID), models, key.ID)
	require.Error(t, err)
}
