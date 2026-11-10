package orchestrator

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

type pageContextExecutor struct {
	mockExecutor
	requests      []*httpclient.Request
	forceFallback bool
}

func (e *pageContextExecutor) Do(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	copy := *req
	copy.Body = slices.Clone(req.Body)
	e.requests = append(e.requests, &copy)
	if !strings.Contains(req.URL, "backup.example") {
		if e.forceFallback {
			return nil, &httpclient.Error{StatusCode: 502, Body: []byte(`{"error":{"message":"fixture unavailable"}}`)}
		}
		for _, item := range gjson.GetBytes(req.Body, "input").Array() {
			if item.Get("type").String() == "function_call_output" && item.Get("call_id").String() == "" {
				return nil, &httpclient.Error{StatusCode: 400, Body: []byte(`{"error":{"type":"invalid_request_error","message":"function_call_output requires call_id on HTTP requests"}}`)}
			}
		}
	}
	return &httpclient.Response{StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}},
		Body: []byte(fmt.Sprintf(`{"id":"resp_page_%d","object":"response","status":"completed","model":"gpt-4","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`, len(e.requests)))}, nil
}

func TestCodexPageContextProductionPipeline(t *testing.T) {
	for _, passThrough := range []bool{false, true} {
		for _, tc := range []struct {
			name                   string
			enabled, forceFallback bool
			wantAttempts           int
		}{
			{"disabled reproduces 400 then fallback", false, false, 2},
			{"enabled succeeds on preferred channel", true, false, 1},
			{"fallback receives original context", true, true, 2},
		} {
			t.Run(fmt.Sprintf("passthrough=%v/%s", passThrough, tc.name), func(t *testing.T) {
				executor := &pageContextExecutor{forceFallback: tc.forceFallback}
				ctx, processor, client, preferred := responsesOriginGateway(t, executor)
				preferred.Channel.Settings = &objects.ChannelSettings{
					PassThroughBody: lo.ToPtr(passThrough), RetryableStatusCodes: []int{400, 502},
					TransformOptions: objects.TransformOptions{CodexPageContextCompatibility: tc.enabled},
				}
				backupRow := client.Channel.Create().SetName("backup").SetType("openai_responses").
					SetBaseURL("https://backup.example/v1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).
					SetSupportedModels([]string{"gpt-4"}).SetDefaultTestModel("gpt-4").SaveX(ctx)
				backupOut, err := responses.NewOutboundTransformer(backupRow.BaseURL, "fixture")
				require.NoError(t, err)
				backup := *preferred
				backup.Channel = &biz.Channel{Channel: backupRow, Outbound: backupOut}
				backup.Channel.Settings = &objects.ChannelSettings{PassThroughBody: lo.ToPtr(passThrough)}
				processor.channelSelector = &staticChannelSelector{candidates: []*ChannelModelsCandidate{preferred, &backup}}
				policy := processor.SystemService.RetryPolicyOrDefault(ctx)
				policy.Enabled, policy.MaxChannelRetries, policy.MaxSingleChannelRetries, policy.RetryDelayMs = true, 1, 0, 0
				policy.LoadBalancerStrategy, policy.TraceStickyMode = biz.LoadBalancerStrategyAdaptive, biz.TraceStickyDisabled
				require.NoError(t, processor.SystemService.SetRetryPolicy(ctx, policy))
				require.NoError(t, processor.SystemService.SetStoragePolicy(ctx, &biz.StoragePolicy{StoreRequestBody: true, StoreResponseBody: true}))
				firstInput := pageContextOpenFixture + "," + pageContextSelectedFixture
				for turn, input := range []string{firstInput, firstInput + `,{"type":"function_call","name":"lookup","call_id":"real","arguments":"{}"},{"type":"function_call_output","call_id":"real","output":"ok"}`} {
					body := []byte(`{"model":"gpt-4","input":[` + input + `,{"role":"user","content":"hello"}]}`)
					start := len(executor.requests)
					_, err := processor.Process(shared.WithSessionScope(ctx, fmt.Sprintf("page-turn-%d", turn)), &httpclient.Request{Method: "POST", Body: body, JSONBody: body})
					require.NoError(t, err)
					sent := executor.requests[start:]
					require.Len(t, sent, tc.wantAttempts)
					typeAtFirst := "function_call_output"
					if tc.enabled {
						typeAtFirst = "message"
					}
					require.Equal(t, typeAtFirst, gjson.GetBytes(sent[0].Body, "input.0.type").String())
					if len(sent) == 2 {
						require.Contains(t, sent[1].URL, "backup.example")
						require.JSONEq(t, pageContextOpenFixture, gjson.GetBytes(sent[1].Body, "input.0").Raw)
					}
					require.Equal(t, "function_call_output", gjson.GetBytes(body, "input.0.type").String())
				}
				// Persistence must show the body that was actually dispatched.
				executions := client.RequestExecution.Query().Order(ent.Asc("id")).AllX(ctx)
				require.Len(t, executions, len(executor.requests))
				for i, execution := range executions {
					stored, err := processor.RequestService.LoadRequestExecutionRequestBody(ctx, execution)
					require.NoError(t, err)
					require.JSONEq(t, string(executor.requests[i].Body), string(stored))
				}
			})
		}
	}
}

func TestCodexPageContextRestoredHistoryWire(t *testing.T) {
	for _, passThrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%v", passThrough), func(t *testing.T) {
			frames := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				frames <- body
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"resp_next","object":"response","status":"completed","model":"gpt-5","output":[]}`))
			}))
			t.Cleanup(server.Close)
			ctx := shared.WithResponsesAttempt(shared.WithSessionScope(t.Context(), "page-restore"), &shared.ResponsesAttempt{})
			store := newResponsesSessionStore()
			root := []byte(`{"model":"gpt-5","input":[` + pageContextOpenFixture + `,` + pageContextSelectedFixture + `,{"role":"user","content":"hello"}]}`)
			store.record(ctx, root, []byte(`{"id":"resp_previous","status":"completed","output":[]}`))
			body := []byte(`{"model":"gpt-5","previous_response_id":"resp_previous","input":"continue"}`)
			p, pipe := responsesCodexWirePipeline(t, server, store, body)
			p.state.ChannelModelsCandidates[0].Channel.Settings = &objects.ChannelSettings{
				PassThroughBody:  lo.ToPtr(passThrough),
				TransformOptions: objects.TransformOptions{CodexPageContextCompatibility: true},
			}
			_, err := pipe.Process(ctx, &httpclient.Request{Method: "POST", Body: body, JSONBody: body, Headers: make(http.Header)})
			require.NoError(t, err)
			sent := <-frames
			require.False(t, gjson.GetBytes(sent, "previous_response_id").Exists())
			items := gjson.GetBytes(sent, "input").Array()
			require.Len(t, items, 4)
			for i, fixture := range []string{pageContextOpenFixture, pageContextSelectedFixture} {
				require.Equal(t, "message", items[i].Get("type").String())
				require.JSONEq(t, fixture, strings.TrimPrefix(items[i].Get("content.0.text").String(), codexPageContextNotice))
			}
			require.Contains(t, items[3].Raw, "continue")
			require.Equal(t, body, p.state.RawRequest.JSONBody)
		})
	}
}
