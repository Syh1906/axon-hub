package orchestrator

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

type responsesReadErrorExecutor struct {
	mockExecutor

	requests     []*httpclient.Request
	withOutput   bool
	unsafeSend   bool
	alwaysFailed bool
}

func (e *responsesReadErrorExecutor) DoStream(_ context.Context, request *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
	copy := *request
	e.requests = append(e.requests, &copy)
	if e.unsafeSend && request.OnReplayUnsafe != nil {
		request.OnReplayUnsafe()
	}
	if len(e.requests) == 1 || e.alwaysFailed {
		events := []*httpclient.StreamEvent{}
		if e.withOutput {
			events = append(events,
				&httpclient.StreamEvent{Type: "response.created", Data: []byte(`{"type":"response.created","response":{"id":"resp_partial","model":"gpt-4","status":"in_progress","output":[]}}`)},
				&httpclient.StreamEvent{Type: "response.output_text.delta", Data: []byte(`{"type":"response.output_text.delta","item_id":"msg_partial","output_index":0,"content_index":0,"delta":"partial"}`)},
			)
		}
		events = append(events, &httpclient.StreamEvent{Type: "error", Data: []byte(`{"type":"error","code":"upstream_stream_read_error","message":"Upstream response stream was interrupted"}`)})
		return streams.SliceStream(events), nil
	}
	return streams.SliceStream([]*httpclient.StreamEvent{
		{Type: "response.completed", Data: []byte(`{"type":"response.completed","response":{"id":"resp_recovered","model":"gpt-4","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)},
	}), nil
}

func TestResponsesReadErrorRetryPreservesOriginAndDeliveryBoundary(t *testing.T) {
	for _, tc := range []struct {
		name         string
		withOutput   bool
		unsafeSend   bool
		alwaysFailed bool
		retries      int
		wantAttempts int
		wantSuccess  bool
	}{
		{name: "retry before output", retries: 1, wantAttempts: 2, wantSuccess: true},
		{name: "output already delivered", withOutput: true, retries: 1, wantAttempts: 1},
		{name: "send outcome unknown", unsafeSend: true, retries: 1, wantAttempts: 1},
		{name: "no retry budget", retries: 0, wantAttempts: 1},
		{name: "retry budget exhausted", alwaysFailed: true, retries: 1, wantAttempts: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := &responsesReadErrorExecutor{withOutput: tc.withOutput, unsafeSend: tc.unsafeSend, alwaysFailed: tc.alwaysFailed}
			executor.response = &httpclient.Response{
				StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": {"application/json"}},
				Body: []byte(`{"id":"resp_read_origin","object":"response","status":"completed","model":"gpt-4","output":[{"id":"rs_origin","type":"reasoning","status":"completed","summary":[],"encrypted_content":"gAAAA_read-origin"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`),
			}
			ctx, processor, client, source := responsesOriginGateway(t, executor)
			source.Channel.Settings = &objects.ChannelSettings{AddressFailover: true, UpstreamAddresses: []objects.ChannelUpstreamAddress{
				{ID: "main", URL: source.Channel.BaseURL, Enabled: true},
				{ID: "backup", URL: "https://backup.example/v1", Priority: 1, Enabled: true},
			}}
			policy := processor.SystemService.RetryPolicyOrDefault(ctx)
			policy.LoadBalancerStrategy = biz.LoadBalancerStrategyAdaptive
			policy.MaxSingleChannelRetries = tc.retries
			require.NoError(t, processor.SystemService.SetRetryPolicy(ctx, policy))
			_, err := processor.Process(ctx, &httpclient.Request{Method: "POST", Body: []byte(`{"model":"gpt-4","input":"first"}`)})
			require.NoError(t, err)
			result, err := processor.Process(ctx, &httpclient.Request{Method: "POST", Body: []byte(`{"model":"gpt-4","stream":true,"input":[{"type":"reasoning","summary":[],"encrypted_content":"gAAAA_read-origin"},{"role":"user","content":"continue"}]}`)})
			sawErrorEvent := false
			if err == nil && result.ChatCompletionStream != nil {
				var events []*httpclient.StreamEvent
				events, err = streams.All(result.ChatCompletionStream)
				for _, event := range events {
					if event.Type == "error" {
						sawErrorEvent = true
					}
				}
				_ = result.ChatCompletionStream.Close()
			}
			if tc.wantSuccess {
				require.NoError(t, err)
				require.False(t, sawErrorEvent)
			} else {
				require.True(t, err != nil || sawErrorEvent, "失败应作为调用错误或流内错误事件返回")
			}
			require.Len(t, executor.requests, tc.wantAttempts)
			for _, request := range executor.requests {
				require.Equal(t, executor.lastRequest.URL, request.URL)
				require.Equal(t, executor.lastRequest.Auth, request.Auth)
				require.Equal(t, executor.requests[0].Body, request.Body)
				require.NotContains(t, request.URL, "backup.example")
			}
			if tc.wantAttempts == 2 {
				executions := client.RequestExecution.Query().Order(ent.Asc("id")).AllX(ctx)
				require.Equal(t, "same_channel", executions[1].RetryDecision)
			}
		})
	}
}
