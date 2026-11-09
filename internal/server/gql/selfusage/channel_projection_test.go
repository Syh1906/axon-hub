package selfusage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestSelfUsageChannelProjection(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:self_usage_channel_projection?mode=memory&cache=shared&_fk=0")
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx := authz.WithTestBypass(context.Background())
	project := db.Project.Create().SetName("projection").SaveX(ctx)
	group := db.APIKeyGroup.Create().SetProjectID(project.ID).SetName("team").SaveX(ctx)
	key := db.APIKey.Create().SetProjectID(project.ID).SetName("member").SetKey("fixture-projection").SetGroupRef(group.ID).SaveX(ctx)
	channel := db.Channel.Create().SetType("openai").SetName("current-channel").
		SetCredentials(objects.ChannelCredentials{APIKey: "private-channel-credential"}).
		SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SetTags([]string{"public-tag"}).SaveX(ctx)
	req := db.Request.Create().SetProjectID(project.ID).SetAPIKeyID(key.ID).SetModelID("model").SetSource("api").SetStatus("completed").
		SetRequestBody([]byte(`{}`)).SetCreatedAt(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)).SaveX(ctx)
	execution := db.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(project.ID).SetChannelID(channel.ID).
		SetModelID("model").SetUpstreamModelID("reported-model").SetStatus("completed").SetRequestBody([]byte(`{}`)).
		SetSelection(&objects.RequestExecutionSelection{ChannelName: "historical-channel", Provider: "openai", KeyHint: "private-hint", KeyRemark: "private-remark"}).
		SetCostSnapshot(&objects.RequestCostSnapshot{KeyRef: "private-key-ref", KeyLabel: "private-key-label", Applied: objects.AppliedCostMultiplier{Value: decimal.RequireFromString("1.75")}}).SaveX(ctx)
	req.Update().SetResultExecutionID(execution.ID).ExecX(ctx)
	system := biz.NewSystemService(biz.SystemServiceParams{Ent: db})
	require.NoError(t, system.SetGeneralSettings(ctx, biz.SystemGeneralSettings{CurrencyCode: "USD", Timezone: "UTC"}))
	h := NewGraphqlHandlers(Dependencies{SelfUsageService: biz.NewSelfUsageService(db, system)})
	query := `{"query":"{ selfUsageRequests(start:\"2026-11-05\",end:\"2026-11-05\") { channelVisible items { channel { name provider channelType tags sessionState multiplier multiplierMixed } requestStatus modelExecutions { modelID upstreamModelID status } } } }"}`
	for _, visible := range []bool{false, true, false} {
		group.Update().SetShowChannelInSelfUsage(visible).ExecX(ctx)
		r := httptest.NewRequest(http.MethodPost, "/self-service/v1/graphql", strings.NewReader(query))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(contexts.WithAPIKey(context.Background(), key))
		w := httptest.NewRecorder()
		h.Graphql.ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code)
		require.NotContains(t, w.Body.String(), `"errors"`)
		var payload struct {
			Data struct {
				SelfUsageRequests struct {
					ChannelVisible bool
					Items          []struct {
						Channel *struct {
							Name, Provider, ChannelType, SessionState string
							Tags                                      []string
							Multiplier                                *float64
							MultiplierMixed                           bool
						}
						RequestStatus   string
						ModelExecutions []struct{ ModelID, UpstreamModelID, Status string }
					}
				}
			}
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
		page := payload.Data.SelfUsageRequests
		require.Equal(t, visible, page.ChannelVisible)
		require.Len(t, page.Items, 1)
		row := page.Items[0]
		if visible {
			require.Equal(t, "historical-channel", row.Channel.Name)
			require.Equal(t, "openai", row.Channel.Provider)
			require.Equal(t, []string{"public-tag"}, row.Channel.Tags)
			require.Equal(t, "new", row.Channel.SessionState)
			require.Equal(t, 1.75, *row.Channel.Multiplier)
		} else {
			require.Nil(t, row.Channel)
			require.NotContains(t, w.Body.String(), "historical-channel")
		}
		require.Equal(t, "completed", row.RequestStatus)
		require.Len(t, row.ModelExecutions, 1)
		require.Equal(t, "model", row.ModelExecutions[0].ModelID)
		require.Equal(t, "reported-model", row.ModelExecutions[0].UpstreamModelID)
		require.Equal(t, "completed", row.ModelExecutions[0].Status)
		for _, hidden := range []string{"private-hint", "private-remark", "private-channel-credential", "private-key-ref", "private-key-label", "channelID", "keyHint", "selection"} {
			require.NotContains(t, w.Body.String(), hidden)
		}
	}
}
