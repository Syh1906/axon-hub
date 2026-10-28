package orchestrator

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	entchannel "github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/stretchr/testify/require"
)

func TestAddressFailoverKeepsModelAndRequest(t *testing.T) {
	ch := &biz.Channel{Channel: &ent.Channel{BaseURL: "https://main.example/v1", Settings: &objects.ChannelSettings{AddressFailover: true, UpstreamAddresses: []objects.ChannelUpstreamAddress{{ID: "b", URL: "https://backup.example/api/v1", Priority: 20, Enabled: true}, {ID: "off", URL: "https://off.example", Priority: 0, Enabled: false}, {ID: "a", URL: "https://main.example/v1", Priority: 10, Enabled: true}}}}}
	p := &PersistentOutboundTransformer{state: &PersistenceState{CurrentCandidate: &ChannelModelsCandidate{Channel: ch, TraceSticky: true}}}
	raw := &httpclient.Request{URL: "https://main.example/v1/responses?test=1", Method: http.MethodPost, Body: []byte(`{"model":"same"}`), Headers: http.Header{"Authorization": []string{"Bearer fixture"}}}
	middleware := applyUpstreamAddress(p)
	first, err := middleware.OnOutboundRawRequest(context.Background(), raw)
	require.NoError(t, err)
	require.Equal(t, raw.URL, first.URL)
	require.True(t, p.CanRetry(&httpclient.Error{StatusCode: 503}))
	require.NoError(t, p.PrepareForRetry(context.Background()))
	second, err := middleware.OnOutboundRawRequest(context.Background(), raw)
	require.NoError(t, err)
	require.Equal(t, "https://backup.example/api/v1/responses?test=1", second.URL)
	require.Equal(t, raw.Headers, second.Headers)
	require.Equal(t, raw.Body, second.Body)
	require.Equal(t, 0, p.state.CurrentModelIndex)
	require.False(t, p.CanRetry(&httpclient.Error{StatusCode: 503}), "地址已耗尽，交回渠道切换")
	for _, code := range []int{401, 403, 429} {
		require.False(t, p.CanRetry(&httpclient.Error{StatusCode: code}))
	}
	require.False(t, isUpstreamAddressFailure(context.Canceled))
	require.False(t, isUpstreamAddressFailure(errors.New("quota exhausted")))
}

type addressRetryKeys struct{ calls int }

func (p *addressRetryKeys) Get(context.Context) string {
	p.calls++
	if p.calls == 1 {
		return "fixture-first-account"
	}
	return "fixture-another-account"
}

func TestAddressFailoverKeepsSelectedKeyThroughTransform(t *testing.T) {
	keys := &addressRetryKeys{}
	outbound, err := openai.NewOutboundTransformerWithConfig(&openai.Config{
		PlatformType: openai.PlatformOpenAI, BaseURL: "https://main.example/v1", APIKeyProvider: biz.NewChannelAPIKeyContextProvider(keys),
	})
	require.NoError(t, err)
	ch := &biz.Channel{Channel: &ent.Channel{Type: entchannel.TypeOpenai, BaseURL: "https://main.example/v1", Settings: &objects.ChannelSettings{
		AddressFailover: true, DisableKeySessionReuse: true,
		UpstreamAddresses: []objects.ChannelUpstreamAddress{{ID: "a", URL: "https://main.example/v1", Enabled: true}, {ID: "b", URL: "https://backup.example/v1", Priority: 1, Enabled: true}},
	}}, Outbound: outbound}
	candidate := &ChannelModelsCandidate{Channel: ch, APIFormat: llm.APIFormatOpenAIChatCompletion.String(), Models: []biz.ChannelModelEntry{{ActualModel: "same-model"}}}
	p := &PersistentOutboundTransformer{state: &PersistenceState{ChannelModelsCandidates: []*ChannelModelsCandidate{candidate}}}
	ctx := contexts.EnsureContainer(context.Background())
	request := &llm.Request{Model: "alias", Messages: []llm.Message{{Role: "user", Content: llm.MessageContent{Content: new("hello")}}}}
	first, err := p.TransformRequest(ctx, request)
	require.NoError(t, err)
	require.Equal(t, "fixture-first-account", first.Auth.APIKey)
	for _, code := range []int{401, 403, 429} {
		require.False(t, p.CanRetryAfterTimeout(&httpclient.Error{StatusCode: code}))
	}
	require.True(t, p.CanRetryAfterTimeout(pipeline.ErrNonStreamResponseTimeout))
	require.NoError(t, p.PrepareForRetry(ctx))
	second, err := p.TransformRequest(ctx, request)
	require.NoError(t, err)
	second, err = applyUpstreamAddress(p).OnOutboundRawRequest(ctx, second)
	require.NoError(t, err)
	require.Equal(t, "https://backup.example/v1/chat/completions", second.URL)
	require.Equal(t, first.Auth.APIKey, second.Auth.APIKey)
	require.Equal(t, first.Body, second.Body)
	require.Equal(t, 1, keys.calls, "切换地址不重新选择账号 Key")
	require.False(t, p.CanRetryAfterTimeout(pipeline.ErrStreamFirstEventTimeout))
}
