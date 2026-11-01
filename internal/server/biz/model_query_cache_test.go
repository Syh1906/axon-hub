package biz

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/stretchr/testify/require"
)

func TestModelAssociationQueryReadsChannelsOnceWithoutCredentials(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:model_query_cache?mode=memory&_fk=0")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(context.Background())
	ch := client.Channel.Create().SetName("catalog").SetType("openai").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).
		SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SetStatus(channel.StatusEnabled).
		SetSettings(&objects.ChannelSettings{ExtraModelPrefix: "prefix"}).SaveX(ctx)
	var statements []string
	debug := ent.NewClient(ent.Driver(client.Driver()), ent.Debug(), ent.Log(func(args ...any) { statements = append(statements, fmt.Sprint(args...)) }))
	service := NewModelService(ModelServiceParams{Ent: debug})
	base := ent.NewContext(ctx, debug)
	query := WithModelAssociationQueryCache(base)
	for i := range 20 {
		m := &ent.Model{ID: i + 1, ModelID: "prefix/model", MatchType: objects.ModelMatchExact, Settings: &objects.ModelSettings{
			Associations: []*objects.ModelAssociation{{Type: "model", ModelID: &objects.ModelIDAssociation{ModelID: "prefix/model"}}},
		}}
		count, err := service.CountModelAssociatedChannels(query, m)
		require.NoError(t, err)
		require.Equal(t, 1, count, "prefix and alias matching must use the existing channel rules")
	}
	var reads int
	for _, statement := range statements {
		if strings.Contains(statement, "FROM `channels`") {
			reads++
			require.NotContains(t, statement, "credentials")
			require.NotContains(t, statement, "error_message")
		}
	}
	require.Equal(t, 1, reads, "20 models share one catalog read, not 20 full-channel reads")
	client.Channel.UpdateOneID(ch.ID).SetStatus(channel.StatusArchived).ExecX(ctx)
	next := WithModelAssociationQueryCache(base)
	count, err := service.CountAssociatedChannels(next, []*objects.ModelAssociation{{Type: "model", ModelID: &objects.ModelIDAssociation{ModelID: "model"}}})
	require.NoError(t, err)
	require.Zero(t, count, "a fresh operation must see changed channel status")
}
