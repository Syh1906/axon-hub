package orchestrator

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
)

func anthropicPreReadEvents(count int, skipped bool) []*httpclient.StreamEvent {
	event := func(kind, data string) *httpclient.StreamEvent {
		return &httpclient.StreamEvent{Type: kind, Data: []byte(data)}
	}
	events := []*httpclient.StreamEvent{
		event("message_start", `{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":1}}}`),
		event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`),
	}
	for range count {
		if skipped {
			events = append(events, event("ping", `{"type":"ping"}`))
		} else {
			events = append(events, event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":""}}`))
		}
	}
	return append(events,
		event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"c2lnbmF0dXJl"}}`),
		event("content_block_stop", `{"type":"content_block_stop","index":0}`),
		event("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`),
		event("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"OK"}}`),
		event("content_block_stop", `{"type":"content_block_stop","index":1}`),
		event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}`),
		event("message_stop", `{"type":"message_stop"}`),
	)
}

func anthropicPreReadPipeline(t *testing.T, executor pipeline.Executor, beforeAttach ...func(*PersistenceState) pipeline.Middleware) func(context.Context, *httpclient.Request) (*pipeline.Result, error) {
	t.Helper()
	provider, err := anthropic.NewOutboundTransformer("https://api.anthropic.com", "test-key")
	require.NoError(t, err)
	candidate := &ChannelModelsCandidate{
		Channel: &biz.Channel{
			Channel:  &ent.Channel{ID: 1, Name: "test", Settings: &objects.ChannelSettings{PassThroughBody: lo.ToPtr(true)}},
			Outbound: provider,
		},
		Models:    []biz.ChannelModelEntry{{RequestModel: "claude-test", ActualModel: "claude-test", Source: "direct"}},
		APIFormat: string(llm.APIFormatAnthropicMessage),
	}
	state := &PersistenceState{OriginalModel: "claude-test", ChannelModelsCandidates: []*ChannelModelsCandidate{candidate}}
	inbound, outbound := NewPersistentTransformers(state, anthropic.NewInboundTransformer())
	var middlewares []pipeline.Middleware
	for _, build := range beforeAttach {
		middlewares = append(middlewares, build(state))
	}
	middlewares = append(middlewares, applyPassThroughStream(outbound, nil), applyPassThroughRequestBody(outbound, nil), captureRawProviderStream(outbound, nil))
	return pipeline.NewFactory(executor).Pipeline(inbound, outbound,
		pipeline.WithRetry(0, 1, 0),
		pipeline.WithMiddlewares(middlewares...),
	).Process
}

func anthropicPreReadRequest() *httpclient.Request {
	return &httpclient.Request{
		Method: http.MethodPost, URL: "/v1/messages", Headers: http.Header{"Content-Type": {"application/json"}},
		Body: []byte(`{"model":"claude-test","max_tokens":1024,"stream":true,"messages":[{"role":"user","content":"OK"}]}`),
	}
}

func TestPassThroughPreReadAnthropic(t *testing.T) {
	for _, skipped := range []bool{false, true} {
		t.Run(fmt.Sprintf("skipped=%t", skipped), func(t *testing.T) {
			events := anthropicPreReadEvents(200, skipped)
			process := anthropicPreReadPipeline(t, &mockExecutor{streamEvents: events})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			result, err := process(ctx, anthropicPreReadRequest())
			require.NoError(t, err)
			defer result.EventStream.Close()
			var actual []*httpclient.StreamEvent
			for result.EventStream.Next() {
				actual = append(actual, result.EventStream.Current())
			}
			require.NoError(t, ctx.Err(), "pre-read must finish before cancellation")
			require.NoError(t, result.EventStream.Err())
			require.Equal(t, events, actual, "raw events must survive attachment in order")
		})
	}
}

func TestPassThroughPreReadSkippedEventsAreBounded(t *testing.T) {
	process := anthropicPreReadPipeline(t, &mockExecutor{streamEvents: anthropicPreReadEvents(maxRawPreAttachEvents, true)})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := process(ctx, anthropicPreReadRequest())
	require.ErrorIs(t, err, pipeline.ErrPreCommitBufferExceeded)
	require.Nil(t, result, "overflow must not expose a partial attempt")
	require.NoError(t, ctx.Err(), "overflow must fail without waiting for the deadline")
}

func TestPassThroughPreReadOverflowBeforeAttachment(t *testing.T) {
	// Signature is meaningful and below the raw limit. Hold attachment until the
	// producer has read ahead beyond that limit, after pre-read already succeeded.
	process := anthropicPreReadPipeline(t,
		&mockExecutor{streamEvents: anthropicPreReadEvents(maxRawPreAttachEvents-5, true)},
		func(state *PersistenceState) pipeline.Middleware {
			return pipeline.OnInboundRawStream("wait-for-producer", func(ctx context.Context, stream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*httpclient.StreamEvent], error) {
				select {
				case <-state.RawStreamDone:
					return stream, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
		},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := process(ctx, anthropicPreReadRequest())
	require.ErrorIs(t, err, pipeline.ErrPreCommitBufferExceeded)
	require.Nil(t, result, "failed pre-attachment capture must not commit buffered content")
	require.NoError(t, ctx.Err())
}

func TestRawStreamBacklogByteBoundary(t *testing.T) {
	event := &httpclient.StreamEvent{Data: make([]byte, 32*1024*1024)}
	for _, overflow := range []bool{false, true} {
		backlog := &rawStreamBacklog{}
		// Legal individual events can exceed the transformed metadata budget.
		for range 2 {
			held, err := backlog.hold(event)
			require.NoError(t, err)
			require.True(t, held)
		}
		if overflow {
			_, err := backlog.hold(&httpclient.StreamEvent{Type: "x"})
			require.ErrorIs(t, err, pipeline.ErrPreCommitBufferExceeded)
		}
		heldEvents, err := backlog.attach()
		if overflow {
			require.ErrorIs(t, err, pipeline.ErrPreCommitBufferExceeded)
			require.Nil(t, heldEvents, "overflowed attempts must not expose a prefix")
			continue
		}
		require.NoError(t, err)
		require.Equal(t, []*httpclient.StreamEvent{event, event}, heldEvents)
		held, err := backlog.hold(event)
		require.NoError(t, err)
		require.False(t, held, "attached streams use channel backpressure, not the pre-read budget")
	}
}
