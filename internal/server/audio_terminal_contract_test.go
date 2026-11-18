package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/api"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/middleware"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/stretchr/testify/require"
)

func TestAudioTerminalHTTPContract(t *testing.T) {
	for _, tc := range []struct {
		name        string
		passThrough bool
		conflict    bool
	}{
		{"conflict", false, true}, {"conflict-pass-through", true, true}, {"valid-pass-through", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCodexRouteHarness(t)
			require.NoError(t, h.system.SetPassThrough(h.ctx, tc.passThrough))
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "event: speech.audio.delta\ndata: "+`{"type":"speech.audio.delta","audio":"YQ=="}`+"\n\n")
				if tc.conflict {
					_, _ = io.WriteString(w, "event: speech.audio.done\ndata: "+`{"type":"speech.audio.delta"}`+"\n\n")
				} else {
					_, _ = io.WriteString(w, "event: speech.audio.done\ndata: "+`{"type":"speech.audio.done","provider_extension":"preserved"}`+"\n\n")
				}
			}))
			t.Cleanup(upstream.Close)
			row := h.db.Channel.Create().SetName("speech").SetType(channel.TypeOpenai).SetBaseURL(upstream.URL).
				SetCredentials(objects.ChannelCredentials{APIKey: "synthetic"}).SetDefaultTestModel("tts").SetSupportedModels([]string{"tts"}).
				SetEndpoints([]objects.ChannelEndpoint{{APIFormat: llm.APIFormatOpenAISpeech.String()}}).SaveX(h.ctx)
			ch, err := h.channels.GetChannel(h.ctx, row.ID)
			require.NoError(t, err)
			h.channels.SetEnabledChannelsForTest([]*biz.Channel{ch})
			h.registerModels(t, ch, "chat")
			orch := *h.orch
			orch.Inbound = openai.NewSpeechInboundTransformer()
			h.openAI.SpeechHandlers = api.NewChatCompletionHandlers(&orch)
			auth := biz.NewAuthService(biz.AuthServiceParams{Ent: h.db, SystemService: h.system, APIKeyService: h.keys})
			const path = "/v1/audio/speech"
			h.router.POST(path, middleware.WithStrictAPIKeyConfig(auth, nil), h.openAI.CreateSpeech)
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"tts","input":"hello","voice":"alloy","stream_format":"sse"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+h.key.Key)
			rec := httptest.NewRecorder()
			h.router.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			body := rec.Body.String()
			require.Contains(t, body, "speech.audio.delta")
			logical, attempt := h.db.Request.Query().OnlyX(h.ctx), h.db.RequestExecution.Query().OnlyX(h.ctx)
			if tc.conflict {
				require.NotContains(t, body, "speech.audio.done")
				require.Contains(t, body, "event:error")
				require.Equal(t, request.StatusFailed, logical.Status)
				require.Equal(t, requestexecution.StatusFailed, attempt.Status)
			} else {
				require.Contains(t, body, "speech.audio.done")
				require.Contains(t, body, "provider_extension")
				require.Equal(t, request.StatusCompleted, logical.Status)
				require.Equal(t, requestexecution.StatusCompleted, attempt.Status)
			}
		})
	}
}
