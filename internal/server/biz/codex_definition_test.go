package biz

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/stretchr/testify/require"
)

func TestCodexDefinitionMultiChannelOfflineAndPermissions(t *testing.T) {
	ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") == "Bearer full" {
			_, _ = w.Write([]byte(catalogFixture))
		} else {
			_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
		}
	}))
	defer server.Close()
	var ids []int
	for _, key := range []string{"full", "list-only", "another-list"} {
		ch := client.Channel.Create().SetType(channel.TypeOpenai).SetName(key).SetBaseURL(server.URL).SetCredentials(objects.ChannelCredentials{APIKey: key}).SetSupportedModels([]string{"test-model"}).SetDefaultTestModel("test-model").SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses"}}).SetStatus(channel.StatusEnabled).SaveX(ctx)
		ids = append(ids, ch.ID)
	}
	createConfiguredModel(t, ctx, client, "test-model", model.TypeChat, &objects.ModelAssociation{Type: "model", ModelID: &objects.ModelIDAssociation{ModelID: "test-model"}})
	reloadEnabledChannels(t, ctx, client, channels)
	svc := NewCodexCatalogService(models, channels)
	automatic, omitted, err := svc.Models(ctx, "0.162.1")
	require.NoError(t, err, "same-model routes automatically share a complete catalog source")
	require.Zero(t, omitted)
	autoCatalog, err := parseCodexCatalog(automatic)
	require.NoError(t, err)
	require.Len(t, autoCatalog, 1)
	listOnlyKey := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "only", Profiles: []objects.APIKeyProfile{{Name: "only", ChannelIDs: []int{ids[1]}}}}}
	automatic, _, err = svc.Models(contexts.WithAPIKey(ctx, listOnlyKey), "0.162.1")
	require.NoError(t, err, "metadata sharing does not require inference permission on its source")
	autoCatalog, err = parseCodexCatalog(automatic)
	require.NoError(t, err)
	require.Equal(t, float64(32000), autoCatalog["test-model"]["context_window"])
	m := client.Model.Query().OnlyX(ctx)
	preview, err := models.PreviewCodexCatalog(ctx, m.ID, "0.162.1")
	require.NoError(t, err)
	d, err := decodeCodexDefinition(preview)
	require.NoError(t, err)
	require.Len(t, d.Routes, 3)
	d.Routes[1].Limits = map[string]any{"context_window": float64(16000)}
	raw, err := json.Marshal(d)
	require.NoError(t, err)
	m.Settings.CodexCatalog = objects.JSONRawMessage(raw)
	require.NoError(t, validateModelSettings(m.Settings))
	_, err = models.UpdateModel(ctx, m.ID, &ent.UpdateModelInput{Settings: m.Settings})
	require.NoError(t, err)
	require.JSONEq(t, string(raw), string(client.Model.GetX(ctx, m.ID).Settings.CodexCatalog))
	server.Close()
	before := calls.Load()
	body, omitted, err := svc.Models(ctx, "0.162.1")
	require.NoError(t, err)
	require.Zero(t, omitted)
	var catalog struct {
		Models []map[string]any `json:"models"`
	}
	require.NoError(t, json.Unmarshal(body, &catalog))
	require.Len(t, catalog.Models, 1)
	require.Equal(t, float64(16000), catalog.Models[0]["context_window"])
	t.Logf("catalog response: %s", body)
	require.Equal(t, before, calls.Load(), "saved definitions must not perform upstream I/O")
	key := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "a", Profiles: []objects.APIKeyProfile{{Name: "a", ChannelIDs: []int{ids[0]}, ModelIDs: []string{"alias"}, ModelMappings: []objects.ModelMapping{{From: "alias", To: "test-model"}}}}}}
	body, _, err = svc.Models(contexts.WithAPIKey(ctx, key), "0.162.1")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(body, &catalog))
	require.Equal(t, "alias", catalog.Models[0]["slug"])
	require.Equal(t, float64(32000), catalog.Models[0]["context_window"], "invisible routes do not shrink capabilities")
	added := client.Channel.Create().SetType(channel.TypeDeepseek).SetName("new-channel").
		SetBaseURL(server.URL).SetCredentials(objects.ChannelCredentials{APIKey: "new-channel"}).
		SetSupportedModels([]string{"test-model"}).SetDefaultTestModel("test-model").
		SetStatus(channel.StatusEnabled).SaveX(ctx)
	reloadEnabledChannels(t, ctx, client, channels)
	body, omitted, err = svc.Models(ctx, "0.162.1")
	require.NoError(t, err)
	require.Zero(t, omitted)
	require.NoError(t, json.Unmarshal(body, &catalog))
	require.Len(t, catalog.Models, 1)
	require.Equal(t, float64(16000), catalog.Models[0]["context_window"], "既有渠道的明确限制仍然生效")
	newChannelKey := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "new", Profiles: []objects.APIKeyProfile{{Name: "new", ChannelIDs: []int{added.ID}}}}}
	body, omitted, err = svc.Models(contexts.WithAPIKey(ctx, newChannelKey), "0.166.0")
	require.NoError(t, err)
	require.Zero(t, omitted)
	require.NoError(t, json.Unmarshal(body, &catalog))
	require.Len(t, catalog.Models, 1)
	require.Equal(t, "test-model", catalog.Models[0]["slug"])
	require.Equal(t, float64(32000), catalog.Models[0]["context_window"], "新增渠道没有目录或快照时使用模型级定义")
	require.JSONEq(t, string(raw), string(client.Model.GetX(ctx, m.ID).Settings.CodexCatalog))
	for _, version := range []string{"0.159.2", "0.163.0", "0.165.0", "0.166.0", "0.167.0"} {
		body, omitted, err = svc.Models(ctx, version)
		require.NoError(t, err, "导入版本是来源记录，不应作为客户端白名单")
		require.Zero(t, omitted)
		require.NoError(t, json.Unmarshal(body, &catalog))
		require.Len(t, catalog.Models, 1)
		require.Equal(t, "test-model", catalog.Models[0]["slug"])
		require.Equal(t, float64(16000), catalog.Models[0]["context_window"])
	}
	require.Equal(t, before, calls.Load(), "兼容读取手动定义不应访问已离线来源")
	d.Routes = nil
	raw, err = json.Marshal(d)
	require.NoError(t, err)
	m.Settings.CodexCatalog = raw
	_, err = models.UpdateModel(ctx, m.ID, &ent.UpdateModelInput{Settings: m.Settings})
	require.NoError(t, err, "保存模型级定义无需填写渠道快照")
	body, omitted, err = svc.Models(ctx, "0.162.1")
	require.NoError(t, err)
	require.Zero(t, omitted)
	require.NoError(t, json.Unmarshal(body, &catalog))
	require.Len(t, catalog.Models, 1)
	require.Equal(t, float64(32000), catalog.Models[0]["context_window"])

	for _, metadata := range []map[string]any{d.Model, d.Models["test-model"].Model} {
		delete(metadata, "context_window")
		delete(metadata, "max_context_window")
		delete(metadata, "auto_compact_token_limit")
	}
	capabilities := codexRouteCapabilities(d.Models["test-model"].Model)
	capabilities["auto_compact_token_limit"] = float64(12000)
	d.Routes = []codexDefinitionRoute{{
		ChannelID: ids[0], ModelID: "test-model", Capabilities: capabilities,
		Limits: map[string]any{"auto_compact_token_limit": float64(10000)},
	}}
	raw, err = json.Marshal(d)
	require.NoError(t, err)
	m.Settings.CodexCatalog = raw
	_, err = models.UpdateModel(ctx, m.ID, &ent.UpdateModelInput{Settings: m.Settings})
	require.NoError(t, err)
	maximum := 16000
	client.Channel.UpdateOneID(ids[0]).SetSettings(&objects.ChannelSettings{
		ModelContexts: []objects.ChannelModelContext{{ModelID: "test-model", MaxContextWindow: &maximum}},
	}).SaveX(ctx)
	reloadEnabledChannels(t, ctx, client, channels)
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want float64
	}{
		{name: "有上下文限制的渠道可见", ctx: ctx, want: float64(16000)},
		{name: "只有未声明上下文的渠道可见", ctx: contexts.WithAPIKey(ctx, newChannelKey)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, omitted, err := svc.Models(tc.ctx, "0.166.0")
			require.NoError(t, err)
			require.Zero(t, omitted)
			entries, err := parseCodexCatalog(body)
			require.NoError(t, err)
			require.Contains(t, entries, "test-model")
			for field, expected := range map[string]float64{
				"context_window": tc.want, "max_context_window": tc.want, "auto_compact_token_limit": 10000,
			} {
				if tc.want == 0 {
					require.NotContains(t, entries["test-model"], field)
				} else {
					require.Equal(t, expected, entries["test-model"][field], field)
				}
			}
		})
	}

	key.Edges.Project = &ent.Project{Profiles: &objects.ProjectProfiles{ActiveProfile: "none", Profiles: []objects.ProjectProfile{{Name: "none", ChannelIDs: []int{99999}}}}}
	body, _, err = svc.Models(contexts.WithAPIKey(ctx, key), "0.162.1")
	require.NoError(t, err)
	require.JSONEq(t, `{"models":[]}`, string(body))
}

func TestCodexDefinitionMergeAndRemoteIsolation(t *testing.T) {
	parsed, err := parseCodexCatalog([]byte(catalogFixture))
	require.NoError(t, err)
	first := parsed["test-model"]
	raw, _ := json.Marshal(first)
	var second map[string]any
	require.NoError(t, json.Unmarshal(raw, &second))
	second["supported_reasoning_levels"] = []any{map[string]any{"effort": "medium", "description": "different text"}}
	second["future_options"] = []any{"unrecognized"}
	first["future_options"] = []any{"keep"}
	merged, err := mergeCodexModels("alias", []map[string]any{first, second})
	require.NoError(t, err)
	require.Equal(t, "medium", merged["default_reasoning_level"])
	require.Equal(t, first["future_options"], merged["future_options"])
	require.Equal(t, first["base_instructions"], merged["base_instructions"])
	second["shell_type"] = "incompatible"
	_, err = mergeCodexModels("alias", []map[string]any{first, second})
	require.Error(t, err)
	mixed := []byte(`{"models":[` + string(raw) + `,{"slug":"broken"},42]}`)
	valid, err := parseRemoteCodexCatalog(mixed)
	require.ErrorIs(t, err, errCodexInvalidEntries)
	require.ErrorContains(t, err, "2 entries omitted")
	require.Len(t, valid, 1)
	_, err = parseCodexCatalog(mixed)
	require.Error(t, err)
	duplicate := []byte(`{"models":[` + string(raw) + `,` + string(raw) + `]}`)
	valid, err = parseRemoteCodexCatalog(duplicate)
	require.ErrorIs(t, err, errCodexInvalidEntries)
	require.ErrorContains(t, err, "2 entries omitted")
	require.Empty(t, valid)
}

func TestCodexDefinitionCurrentClientSchema(t *testing.T) {
	raw, err := os.ReadFile("testdata/codex-0.166.0.json")
	require.NoError(t, err)
	models, err := parseCodexCatalog(raw)
	require.NoError(t, err)
	source := models["gpt-6.1-sol"]
	require.NotNil(t, source)
	changedJSON, _ := json.Marshal(source)
	var restricted map[string]any
	require.NoError(t, json.Unmarshal(changedJSON, &restricted))
	restricted["supports_image_detail_original"] = false
	merged, err := mergeCodexModels("alias", []map[string]any{source, restricted})
	require.NoError(t, err)
	require.Equal(t, false, merged["supports_image_detail_original"])
	for _, field := range []string{"model_messages", "node_repl_auto_review_required", "availability_nux", "multi_agent_reasoning_effort", "include_plugin_usage_instructions"} {
		require.Equal(t, source[field], merged[field], "preserve current client field %s", field)
	}
}

func TestCodexDefinitionImportedCapabilitiesStayKeyScoped(t *testing.T) {
	for _, order := range [][]string{{"small", "large"}, {"large", "small"}} {
		t.Run(strings.Join(order, "-"), func(t *testing.T) {
			ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := catalogFixture
				if r.Header.Get("Authorization") == "Bearer small" {
					body = strings.ReplaceAll(body, "32000", "16000")
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			ids := map[string]int{}
			for _, name := range order {
				ch := client.Channel.Create().SetType(channel.TypeOpenai).SetName(name).SetBaseURL(server.URL).SetCredentials(objects.ChannelCredentials{APIKey: name}).SetSupportedModels([]string{"test-model"}).SetDefaultTestModel("test-model").SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses"}}).SetStatus(channel.StatusEnabled).SaveX(ctx)
				ids[name] = ch.ID
			}
			createConfiguredModel(t, ctx, client, "test-model", model.TypeChat, &objects.ModelAssociation{Type: "model", ModelID: &objects.ModelIDAssociation{ModelID: "test-model"}})
			reloadEnabledChannels(t, ctx, client, channels)
			m := client.Model.Query().OnlyX(ctx)
			raw, err := models.PreviewCodexCatalog(ctx, m.ID, "0.166.0")
			require.NoError(t, err)
			m.Settings.CodexCatalog = raw
			client.Model.UpdateOneID(m.ID).SetSettings(m.Settings).SaveX(ctx)
			svc := NewCodexCatalogService(models, channels)
			for name, want := range map[string]float64{"small": 16000, "large": 32000} {
				key := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "only", Profiles: []objects.APIKeyProfile{{Name: "only", ChannelIDs: []int{ids[name]}}}}}
				body, _, err := svc.Models(contexts.WithAPIKey(ctx, key), "0.166.0")
				require.NoError(t, err)
				var result struct {
					Models []map[string]any `json:"models"`
				}
				require.NoError(t, json.Unmarshal(body, &result))
				require.Len(t, result.Models, 1)
				require.Equal(t, want, result.Models[0]["context_window"])
			}
		})
	}
}

func TestCodexDefinitionRejectsMalformedKnownFieldsAndDefaults(t *testing.T) {
	parsed, err := parseCodexCatalog([]byte(catalogFixture))
	require.NoError(t, err)
	good := parsed["test-model"]
	for name, badValue := range map[string]any{"shell_type": "invalid", "supported_reasoning_levels": []any{map[string]any{"effort": 42, "description": "bad"}}, "truncation_policy": map[string]any{}, "priority": 0.5, "experimental_supported_tools": []any{42}, "input_modalities": []any{"invalid"}, "model_messages": map[string]any{"instructions_template": 42}} {
		t.Run(name, func(t *testing.T) {
			copyJSON, _ := json.Marshal(good)
			var bad map[string]any
			require.NoError(t, json.Unmarshal(copyJSON, &bad))
			bad["slug"] = "broken"
			bad[name] = badValue
			mixed, _ := json.Marshal(map[string]any{"models": []any{good, bad}})
			filtered, err := parseRemoteCodexCatalog(mixed)
			require.ErrorIs(t, err, errCodexInvalidEntries)
			require.Len(t, filtered, 1)
			require.Contains(t, filtered, "test-model")
			definitionJSON, _ := json.Marshal(codexDefinition{ClientVersion: "0.166.0", Model: bad, Routes: []codexDefinitionRoute{{ChannelID: 1, ModelID: "broken"}}})
			require.Error(t, validateModelSettings(&objects.ModelSettings{CodexCatalog: definitionJSON}))
			definitionJSON, _ = json.Marshal(codexDefinition{ClientVersion: "0.166.0", Model: good, Models: map[string]codexModelProfile{"broken": {SourceChannelID: 1, SourceModelID: "broken", Model: bad}}, Routes: []codexDefinitionRoute{{ChannelID: 1, ModelID: "broken"}}})
			require.Error(t, validateModelSettings(&objects.ModelSettings{CodexCatalog: definitionJSON}))
		})
	}
	for _, field := range []string{"supports_image_detail_original", "supports_search_tool", "supports_experimental_context", "use_responses_lite", "supports_reasoning_effort_updates"} {
		copyJSON, _ := json.Marshal(good)
		var first, second map[string]any
		require.NoError(t, json.Unmarshal(copyJSON, &first))
		require.NoError(t, json.Unmarshal(copyJSON, &second))
		first[field] = true
		delete(second, field)
		merged, err := mergeCodexModels("alias", []map[string]any{first, second})
		require.NoError(t, err)
		require.Equal(t, false, merged[field])
	}
}

func TestCodexDefinitionLimitsAndNestedKnownFields(t *testing.T) {
	parsed, err := parseCodexCatalog([]byte(catalogFixture))
	require.NoError(t, err)
	good := parsed["test-model"]
	for _, change := range []map[string]any{
		{"availability_nux": map[string]any{"message": 42}},
		{"experimental_supported_tools": []any{nil}},
		{"model_messages": map[string]any{"instructions_template": "fixture", "approvals": 42}},
		{"model_messages": map[string]any{"instructions_template": "fixture", "approvals": map[string]any{"on_request": 42}}},
		{"model_specialty": 42}, {"auto_review_model_override": 42},
		{"model_messages": map[string]any{"instructions_template": "fixture", "permissions": map[string]any{"read_only": 42}}},
		{"model_messages": map[string]any{"instructions_template": "fixture", "token_budget": map[string]any{}}},
		{"model_messages": map[string]any{"instructions_template": "fixture", "tools": map[string]any{"history": map[string]any{"read_item": map[string]any{"description": 42}}}}},
	} {
		b, _ := json.Marshal(good)
		var bad map[string]any
		require.NoError(t, json.Unmarshal(b, &bad))
		bad["slug"] = "broken"
		for k, v := range change {
			bad[k] = v
		}
		mixed, _ := json.Marshal(map[string]any{"models": []any{good, bad}})
		filtered, err := parseRemoteCodexCatalog(mixed)
		require.ErrorIs(t, err, errCodexInvalidEntries)
		require.Len(t, filtered, 1)
	}
	raw, _ := json.Marshal(codexDefinition{ClientVersion: "0.166.0", Model: good, Routes: []codexDefinitionRoute{{ChannelID: 1, ModelID: "test-model", Limits: map[string]any{"context_window": 0.5}}}})
	require.Error(t, validateModelSettings(&objects.ModelSettings{CodexCatalog: raw}))
	for _, alias := range []string{"default", "local", "shell_command"} {
		b, _ := json.Marshal(good)
		var other map[string]any
		require.NoError(t, json.Unmarshal(b, &other))
		other["shell_type"] = alias
		delete(other, "web_search_tool_type")
		b, _ = json.Marshal(map[string]any{"models": []any{other}})
		parsed, err := parseCodexCatalog(b)
		require.NoError(t, err)
		_, err = mergeCodexModels("alias", []map[string]any{good, parsed["test-model"]})
		require.NoError(t, err)
	}
}

func TestCodexDefinitionRegexAndConditionalBindings(t *testing.T) {
	ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
	metadata, _ := parseCodexCatalog([]byte(catalogFixture))
	base := metadata["test-model"]
	var entries []map[string]any
	for _, id := range []string{"registered-a", "registered-b"} {
		b, _ := json.Marshal(base)
		var entry map[string]any
		require.NoError(t, json.Unmarshal(b, &entry))
		entry["slug"] = id
		entries = append(entries, entry)
	}
	catalog, _ := json.Marshal(map[string]any{"models": entries})
	ch := client.Channel.Create().SetType(channel.TypeOpenai).SetName("pattern").SetBaseURL("http://127.0.0.1:1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SetSupportedModels([]string{"registered-a", "registered-b"}).SetDefaultTestModel("registered-a").SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", CatalogJSON: catalog}}).SetStatus(channel.StatusEnabled).SaveX(ctx)
	createConfiguredModel(t, ctx, client, "^registered-.*$", model.TypeChat, &objects.ModelAssociation{Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: ch.ID}, When: &objects.ModelAssociationWhen{Enabled: true, Condition: &objects.Condition{Type: "condition", Field: "request_format", Operator: "eq", Value: "openai/responses"}}})
	m := client.Model.Query().OnlyX(ctx)
	m = client.Model.UpdateOneID(m.ID).SetMatchType(objects.ModelMatchType("regex")).SetPublicModelIds([]string{"registered-a", "registered-b"}).SaveX(ctx)
	reloadEnabledChannels(t, ctx, client, channels)
	raw, err := models.PreviewCodexCatalog(ctx, m.ID, "0.166.0")
	require.NoError(t, err)
	d, err := decodeCodexDefinition(raw)
	require.NoError(t, err)
	require.Len(t, d.Routes, 2)
	m.Settings.CodexCatalog = raw
	client.Model.UpdateOneID(m.ID).SetSettings(m.Settings).SaveX(ctx)
	key := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "a", Profiles: []objects.APIKeyProfile{{Name: "a", ModelIDs: []string{"public-alias"}, ModelMappings: []objects.ModelMapping{{From: "public-alias", To: "registered-b"}}}}}}
	body, _, err := NewCodexCatalogService(models, channels).Models(contexts.WithAPIKey(ctx, key), "0.166.0")
	require.NoError(t, err)
	var result struct {
		Models []map[string]any `json:"models"`
	}
	require.NoError(t, json.Unmarshal(body, &result))
	require.Len(t, result.Models, 1)
	require.Equal(t, "public-alias", result.Models[0]["slug"])
}

func TestCodexDefinitionHeterogeneousPublicProfiles(t *testing.T) {
	ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
	metadata, err := parseCodexCatalog([]byte(catalogFixture))
	require.NoError(t, err)
	baseJSON, err := json.Marshal(metadata["test-model"])
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer list-only" {
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-6.1-sol"},{"id":"gpt-5.6-sol"}]}`))
			return
		}
		var entries []map[string]any
		for _, id := range []string{"gpt-6.1-sol", "gpt-5.6-sol"} {
			var entry map[string]any
			require.NoError(t, json.Unmarshal(baseJSON, &entry))
			entry["slug"] = id
			entry["base_instructions"] = "fixture instructions for " + id
			entry["web_search_tool_type"] = "text"
			if id == "gpt-6.1-sol" {
				entry["apply_patch_tool_type"] = "freeform"
				entry["web_search_tool_type"] = "text_and_image"
				entry["tool_mode"] = "code_mode_only"
				entry["multi_agent_version"] = "v2"
			}
			if r.Header.Get("Authorization") == "Bearer second" {
				entry["apply_patch_tool_type"] = "freeform"
				entry["web_search_tool_type"] = "text"
				entry["tool_mode"] = nil
				entry["multi_agent_version"] = nil
				entry["context_window"] = float64(16000)
				entry["truncation_policy"] = map[string]any{"mode": "bytes", "limit": float64(4000)}
			}
			entries = append(entries, entry)
		}
		body, marshalErr := json.Marshal(map[string]any{"models": entries})
		require.NoError(t, marshalErr)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	var associations []*objects.ModelAssociation
	var secondID int
	for _, name := range []string{"first", "second", "list-only"} {
		ch := client.Channel.Create().SetType(channel.TypeOpenaiResponses).SetName(name).SetBaseURL(server.URL).SetCredentials(objects.ChannelCredentials{APIKey: name}).SetSupportedModels([]string{"gpt-6.1-sol", "gpt-5.6-sol"}).SetDefaultTestModel("gpt-6.1-sol").SetStatus(channel.StatusEnabled).SaveX(ctx)
		associations = append(associations, &objects.ModelAssociation{Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: ch.ID}})
		if name == "second" {
			secondID = ch.ID
		}
	}
	createConfiguredModel(t, ctx, client, "^gpt-.*-sol$", model.TypeChat, associations[0])
	m := client.Model.Query().OnlyX(ctx)
	m.Settings.Associations = associations
	m = client.Model.UpdateOneID(m.ID).SetSettings(m.Settings).SetMatchType(objects.ModelMatchType("regex")).SetPublicModelIds([]string{"gpt-6.1-sol", "gpt-5.6-sol"}).SaveX(ctx)
	reloadEnabledChannels(t, ctx, client, channels)
	autoService := NewCodexCatalogService(models, channels)
	automatic, omitted, err := autoService.Models(ctx, "0.166.0")
	require.NoError(t, err)
	require.Zero(t, omitted)
	autoCatalog, err := parseCodexCatalog(automatic)
	require.NoError(t, err)
	require.Len(t, autoCatalog, 2)
	require.Equal(t, "code_mode_only", autoCatalog["gpt-6.1-sol"]["tool_mode"])
	require.Nil(t, autoCatalog["gpt-5.6-sol"]["apply_patch_tool_type"])
	require.Equal(t, float64(16000), autoCatalog["gpt-6.1-sol"]["context_window"])
	require.Empty(t, client.Model.GetX(ctx, m.ID).Settings.CodexCatalog, "automatic reads never persist a manual override")
	t.Logf("automatic wire catalog: %s", automatic)
	raw, err := models.PreviewCodexCatalog(ctx, m.ID, "0.166.0")
	require.NoError(t, err, "different concrete versions and source tool policies must produce a reviewable preview")
	d, err := decodeCodexDefinition(raw)
	require.NoError(t, err)
	require.Len(t, d.Models, 2)
	require.Len(t, d.Routes, 6)
	require.NotEmpty(t, d.SourceDifferences)
	for _, route := range d.Routes {
		require.NotContains(t, route.Capabilities, "truncation_policy")
	}
	var fields []string
	for _, difference := range d.SourceDifferences {
		fields = append(fields, difference.Fields...)
	}
	for _, field := range []string{"apply_patch_tool_type", "web_search_tool_type", "tool_mode", "multi_agent_version", "truncation_policy"} {
		require.Contains(t, fields, field)
	}
	// Published .syh.11 snapshots contained this field; it must not force the
	// selected client profile to use a different clipping unit.
	d.Routes[1].Capabilities["truncation_policy"] = map[string]any{"mode": "bytes", "limit": float64(4000)}
	raw, err = json.Marshal(d)
	require.NoError(t, err)
	m.Settings.CodexCatalog = raw
	_, err = models.UpdateModel(ctx, m.ID, &ent.UpdateModelInput{Settings: m.Settings})
	require.NoError(t, err)
	server.Close()
	svc := NewCodexCatalogService(models, channels)
	body, omitted, err := svc.Models(ctx, "0.166.0")
	require.NoError(t, err)
	require.Zero(t, omitted)
	parsed, err := parseCodexCatalog(body)
	require.NoError(t, err)
	require.Len(t, parsed, 2)
	for id, entry := range parsed {
		require.Equal(t, "fixture instructions for "+id, entry["base_instructions"])
		require.Equal(t, float64(16000), entry["context_window"])
		require.Equal(t, "tokens", entry["truncation_policy"].(map[string]any)["mode"])
	}
	t.Logf("wire catalog: %s", body)
	require.Nil(t, parsed["gpt-5.6-sol"]["apply_patch_tool_type"])
	require.Equal(t, "code_mode_only", parsed["gpt-6.1-sol"]["tool_mode"])
	key := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "alias", Profiles: []objects.APIKeyProfile{{Name: "alias", ChannelIDs: []int{secondID}, ModelIDs: []string{"client-alias"}, ModelMappings: []objects.ModelMapping{{From: "client-alias", To: "gpt-6.1-sol"}}}}}}
	body, _, err = svc.Models(contexts.WithAPIKey(ctx, key), "0.166.0")
	require.NoError(t, err)
	parsed, err = parseCodexCatalog(body)
	require.NoError(t, err)
	require.Len(t, parsed, 1)
	require.Equal(t, "fixture instructions for gpt-6.1-sol", parsed["client-alias"]["base_instructions"])
	require.Equal(t, "code_mode_only", parsed["client-alias"]["tool_mode"])
}
