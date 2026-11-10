package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	responsestransformer "github.com/looplj/axonhub/llm/transformer/openai/responses"
)

// === captureRawProviderStream tests ===

func TestCaptureRawProviderStream_Disabled(t *testing.T) {
	ctx := context.Background()
	channel := &biz.Channel{
		Channel: &ent.Channel{ID: 1, Name: "test"},
	}
	state := &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
	}
	outbound := &PersistentOutboundTransformer{state: state}

	mw := captureRawProviderStream(outbound)
	original := testHTTPStream(nil)

	result, err := mw.OnOutboundRawStream(ctx, original)
	require.NoError(t, err)
	assert.Equal(t, original, result)
}

func TestCaptureRawProviderStream_NilLlmRequest(t *testing.T) {
	ctx := context.Background()
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}
	state := &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
	}
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{apiFormat: llm.APIFormatOpenAIChatCompletion},
		state:   state,
	}

	mw := captureRawProviderStream(outbound)
	original := testHTTPStream(nil)

	result, err := mw.OnOutboundRawStream(ctx, original)
	require.NoError(t, err)
	assert.Equal(t, original, result)
}

func TestCaptureRawProviderStream_RecordsPreReadAndLiveEvents(t *testing.T) {
	ctx := context.Background()

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}
	state := &PersistenceState{
		CurrentCandidate:      &ChannelModelsCandidate{Channel: channel},
		OriginalRequestStream: lo.ToPtr(true),
		LlmRequest: &llm.Request{
			APIFormat: llm.APIFormatOpenAIChatCompletion,
			Stream:    lo.ToPtr(true),
			RawRequest: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIChatCompletion),
				Body:      []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
			},
		},
		RawProviderRequest: &httpclient.Request{
			APIFormat: string(llm.APIFormatOpenAIChatCompletion),
		},
	}
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{apiFormat: llm.APIFormatOpenAIChatCompletion},
		state:   state,
	}

	// The pipeline pre-reads part of the attempt before the pass-through consumer
	// exists; the rest of the stream is read while the consumer pulls it.
	const preReadCount = 100
	events := make([]*httpclient.StreamEvent, preReadCount+3)
	for i := range events {
		events[i] = &httpclient.StreamEvent{Data: fmt.Appendf(nil, `{"id":"evt%d"}`, i+1)}
	}

	captured, err := captureRawProviderStream(outbound).OnOutboundRawStream(ctx, testHTTPStream(events))
	require.NoError(t, err)
	require.NotNil(t, state.PassThroughRecorder)

	var preRead []*httpclient.StreamEvent
	for range preReadCount {
		require.True(t, captured.Next())
		preRead = append(preRead, captured.Current())
	}

	var pipelineEvents []*httpclient.StreamEvent
	transformed := streams.Map(streams.PrependStream(captured, preRead...), func(event *httpclient.StreamEvent) *httpclient.StreamEvent {
		pipelineEvents = append(pipelineEvents, event)
		return event
	})

	passThrough, err := applyPassThroughStream(outbound).OnInboundRawStream(ctx, transformed)
	require.NoError(t, err)

	var passthroughEvents []*httpclient.StreamEvent
	for passThrough.Next() {
		passthroughEvents = append(passthroughEvents, passThrough.Current())
	}

	require.NoError(t, passThrough.Err())
	assert.Equal(t, events, pipelineEvents)
	assert.Equal(t, events, passthroughEvents)
}

func TestCaptureRawProviderStream_PropagatesError(t *testing.T) {
	ctx := context.Background()
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}
	state := &PersistenceState{
		CurrentCandidate:      &ChannelModelsCandidate{Channel: channel},
		OriginalRequestStream: lo.ToPtr(true),
		LlmRequest: &llm.Request{
			APIFormat: llm.APIFormatOpenAIChatCompletion,
			Stream:    lo.ToPtr(true),
			RawRequest: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIChatCompletion),
				Body:      []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
			},
		},
		RawProviderRequest: &httpclient.Request{
			APIFormat: string(llm.APIFormatOpenAIChatCompletion),
		},
	}
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{apiFormat: llm.APIFormatOpenAIChatCompletion},
		state:   state,
	}

	errTest := errors.New("stream error")
	src := &errorStream{err: errTest}

	mw := captureRawProviderStream(outbound)
	result, err := mw.OnOutboundRawStream(ctx, src)
	require.NoError(t, err)

	for result.Next() { //nolint:revive // intentional drain
	}

	assert.Equal(t, errTest, result.Err())
	assert.Equal(t, errTest, state.PassThroughRecorder.Err())
}

func TestCaptureRawProviderStream_ResetReleasesUpstream(t *testing.T) {
	ctx := context.Background()
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}
	state := &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
		LlmRequest:       &llm.Request{APIFormat: llm.APIFormatOpenAIChatCompletion},
		RawProviderRequest: &httpclient.Request{
			APIFormat: string(llm.APIFormatOpenAIChatCompletion),
		},
	}
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{apiFormat: llm.APIFormatOpenAIChatCompletion},
		state:   state,
	}

	src := newBlockingStream()
	_, err := captureRawProviderStream(outbound).OnOutboundRawStream(ctx, src)
	require.NoError(t, err)
	require.NotNil(t, state.PassThroughRecorder)

	// A retry abandons the attempt: resetting the pass-through state must release
	// the upstream stream even though nothing reads it any more.
	outbound.resetPassThroughStreamState()
	assert.Nil(t, state.PassThroughRecorder)

	select {
	case <-src.closed:
	case <-time.After(time.Second):
		t.Fatal("upstream stream was not closed")
	}
}

func TestCaptureRawProviderStream_UsesRawProviderRequestAPIFormat(t *testing.T) {
	ctx := context.Background()
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}
	state := &PersistenceState{
		CurrentCandidate:      &ChannelModelsCandidate{Channel: channel},
		OriginalRequestStream: lo.ToPtr(true),
		LlmRequest: &llm.Request{
			APIFormat: llm.APIFormatOpenAIChatCompletion,
			Stream:    lo.ToPtr(true),
			RawRequest: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIChatCompletion),
				Body:      []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
			},
		},
		RawProviderRequest: &httpclient.Request{
			APIFormat: string(llm.APIFormatOpenAIChatCompletion),
		},
	}
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{apiFormat: llm.APIFormatAnthropicMessage},
		state:   state,
	}

	mw := captureRawProviderStream(outbound)
	original := testHTTPStream(nil)

	result, err := mw.OnOutboundRawStream(ctx, original)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotEqual(t, original, result)
	assert.NotNil(t, state.PassThroughRecorder)
}

type errorStream struct {
	err error
}

func (s *errorStream) Next() bool                       { return false }
func (s *errorStream) Current() *httpclient.StreamEvent { return nil }
func (s *errorStream) Err() error                       { return s.err }
func (s *errorStream) Close() error                     { return nil }

type blockingStream struct {
	started   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func newBlockingStream() *blockingStream {
	return &blockingStream{
		started: make(chan struct{}),
		closed:  make(chan struct{}),
	}
}

func (s *blockingStream) Next() bool {
	s.startOnce.Do(func() {
		close(s.started)
	})
	<-s.closed

	return false
}

func (s *blockingStream) Current() *httpclient.StreamEvent { return nil }
func (s *blockingStream) Err() error                       { return nil }
func (s *blockingStream) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
	})

	return nil
}

// === applyPassThroughStream tests ===

func TestApplyPassThroughStream_Disabled(t *testing.T) {
	ctx := context.Background()
	channel := &biz.Channel{
		Channel: &ent.Channel{ID: 1, Name: "test"},
	}
	state := &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
	}
	outbound := &PersistentOutboundTransformer{state: state}

	mw := applyPassThroughStream(outbound)
	transformed := testHTTPStream(nil)

	result, err := mw.OnInboundRawStream(ctx, transformed)
	require.NoError(t, err)
	assert.Equal(t, transformed, result)
}

func TestApplyPassThroughStream_NoRecorder(t *testing.T) {
	ctx := context.Background()
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}
	state := &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
	}
	outbound := &PersistentOutboundTransformer{state: state}

	mw := applyPassThroughStream(outbound)
	transformed := testHTTPStream(nil)

	result, err := mw.OnInboundRawStream(ctx, transformed)
	require.NoError(t, err)
	assert.Equal(t, transformed, result)
}

func passThroughChatState() *PersistenceState {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}

	return &PersistenceState{
		CurrentCandidate:      &ChannelModelsCandidate{Channel: channel},
		OriginalRequestStream: lo.ToPtr(true),
		LlmRequest: &llm.Request{
			APIFormat: llm.APIFormatOpenAIChatCompletion,
			Stream:    lo.ToPtr(true),
			RawRequest: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIChatCompletion),
				Body:      []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
			},
		},
		RawProviderRequest: &httpclient.Request{
			APIFormat: string(llm.APIFormatOpenAIChatCompletion),
		},
	}
}

// reencode stands in for the transform chain: it re-encodes every raw event it reads.
func reencode(stream streams.Stream[*httpclient.StreamEvent]) streams.Stream[*httpclient.StreamEvent] {
	return streams.Map(stream, func(event *httpclient.StreamEvent) *httpclient.StreamEvent {
		return &httpclient.StreamEvent{Data: append([]byte("transformed:"), event.Data...)}
	})
}

func TestApplyPassThroughStream_ReturnsRawEvents(t *testing.T) {
	ctx := context.Background()
	state := passThroughChatState()
	outbound := &PersistentOutboundTransformer{state: state}

	rawEvents := []*httpclient.StreamEvent{
		{Data: json.RawMessage(`{"id":"r1"}`)},
		{Data: json.RawMessage(`{"id":"r2"}`)},
	}
	captured, err := captureRawProviderStream(outbound).OnOutboundRawStream(ctx, testHTTPStream(rawEvents))
	require.NoError(t, err)

	result, err := applyPassThroughStream(outbound).OnInboundRawStream(ctx, reencode(captured))
	require.NoError(t, err)

	var passthroughEvents []*httpclient.StreamEvent
	for result.Next() {
		passthroughEvents = append(passthroughEvents, result.Current())
	}

	require.NoError(t, result.Err())
	assert.Equal(t, rawEvents, passthroughEvents)
}

func TestApplyPassThroughStream_DrainsTransformed(t *testing.T) {
	ctx := context.Background()
	state := passThroughChatState()
	outbound := &PersistentOutboundTransformer{state: state}

	captured, err := captureRawProviderStream(outbound).OnOutboundRawStream(ctx, testHTTPStream([]*httpclient.StreamEvent{
		{Data: json.RawMessage(`{"id":"r1"}`)},
	}))
	require.NoError(t, err)

	drained := make(chan struct{})
	transformed := &doneStream{stream: reencode(captured), done: drained}

	result, err := applyPassThroughStream(outbound).OnInboundRawStream(ctx, transformed)
	require.NoError(t, err)

	for result.Next() {
	}

	select {
	case <-drained:
	default:
		t.Fatal("transformed stream was not drained")
	}
}

func TestApplyPassThroughStream_ForwardsDroppedRawEventsInOrder(t *testing.T) {
	ctx := context.Background()
	state := passThroughChatState()
	outbound := &PersistentOutboundTransformer{state: state}

	rawEvents := []*httpclient.StreamEvent{
		{Type: "ping", Data: json.RawMessage(`{"type":"ping"}`)},
		{Data: json.RawMessage(`{"id":"r1"}`)},
		{Type: "ping", Data: json.RawMessage(`{"type":"ping"}`)},
		{Data: json.RawMessage(`{"id":"r2"}`)},
		{Type: "ping", Data: json.RawMessage(`{"type":"ping"}`)},
	}
	captured, err := captureRawProviderStream(outbound).OnOutboundRawStream(ctx, testHTTPStream(rawEvents))
	require.NoError(t, err)

	// The transform drops pings, so they reach the client with the next transformed
	// event, and trailing ones once the transformed stream ends.
	transformed := reencode(streams.Filter(captured, func(event *httpclient.StreamEvent) bool {
		return event.Type != "ping"
	}))
	result, err := applyPassThroughStream(outbound).OnInboundRawStream(ctx, transformed)
	require.NoError(t, err)

	var passthroughEvents []*httpclient.StreamEvent
	for result.Next() {
		passthroughEvents = append(passthroughEvents, result.Current())
	}

	require.NoError(t, result.Err())
	assert.Equal(t, rawEvents, passthroughEvents)
}

func TestApplyPassThroughStream_TransformErrorAfterRawEvents(t *testing.T) {
	ctx := context.Background()
	state := passThroughChatState()
	outbound := &PersistentOutboundTransformer{state: state}

	rawEvents := []*httpclient.StreamEvent{
		{Data: json.RawMessage(`{"id":"r1"}`)},
		{Type: "error", Data: json.RawMessage(`{"type":"error","error":{"type":"overloaded_error"}}`)},
	}
	captured, err := captureRawProviderStream(outbound).OnOutboundRawStream(ctx, testHTTPStream(rawEvents))
	require.NoError(t, err)

	errOverloaded := errors.New("overloaded")
	transformed := streams.MapErr(captured, func(event *httpclient.StreamEvent) (*httpclient.StreamEvent, error) {
		if event.Type == "error" {
			return nil, errOverloaded
		}
		return event, nil
	})
	result, err := applyPassThroughStream(outbound).OnInboundRawStream(ctx, transformed)
	require.NoError(t, err)

	var passthroughEvents []*httpclient.StreamEvent
	for result.Next() {
		passthroughEvents = append(passthroughEvents, result.Current())
	}

	// The raw error event still reaches the client before the transform error.
	assert.Equal(t, rawEvents, passthroughEvents)
	require.ErrorIs(t, result.Err(), errOverloaded)
}

type doneStream struct {
	stream streams.Stream[*httpclient.StreamEvent]
	done   chan struct{}
}

func (s *doneStream) Next() bool {
	ok := s.stream.Next()
	if !ok {
		close(s.done)
	}

	return ok
}

func (s *doneStream) Current() *httpclient.StreamEvent { return s.stream.Current() }
func (s *doneStream) Err() error                       { return s.stream.Err() }
func (s *doneStream) Close() error                     { return s.stream.Close() }

// === Integration: LLM stream middleware runs during stream pass-through ===

// trackingLLM is a middleware that verifies OnOutboundLlmStream is called during pass-through.
type trackingLLM struct {
	pipeline.DummyMiddleware

	called    bool
	evtCount  int
	closeCall bool
	mu        sync.Mutex
}

func (m *trackingLLM) Name() string { return "tracking-llm" }

func (m *trackingLLM) OnOutboundLlmStream(ctx context.Context, stream streams.Stream[*llm.Response]) (streams.Stream[*llm.Response], error) {
	m.mu.Lock()
	m.called = true
	m.mu.Unlock()

	return &trackingWrapper{
		stream: stream,
		mw:     m,
	}, nil
}

// eventCount returns the current event count under the mutex so tests can
// safely read it without racing with trackingWrapper.Next.
func (m *trackingLLM) eventCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.evtCount
}

type trackingWrapper struct {
	stream streams.Stream[*llm.Response]
	mw     *trackingLLM
}

func (w *trackingWrapper) Next() bool {
	ok := w.stream.Next()
	if ok {
		w.mw.mu.Lock()
		w.mw.evtCount++
		w.mw.mu.Unlock()
	}

	return ok
}

func (w *trackingWrapper) Current() *llm.Response { return w.stream.Current() }
func (w *trackingWrapper) Err() error             { return w.stream.Err() }

func (w *trackingWrapper) Close() error {
	w.mw.mu.Lock()
	w.mw.closeCall = true
	w.mw.mu.Unlock()

	return w.stream.Close()
}

// passthroughOutbound is an outbound transformer that maps raw events 1:1 to llm responses.
type passthroughOutbound struct {
	format llm.APIFormat
}

type passthroughPolicyOutbound struct {
	passthroughOutbound

	allow bool
}

func (t *passthroughOutbound) APIFormat() llm.APIFormat { return t.format }

func (t *passthroughOutbound) TransformRequest(ctx context.Context, req *llm.Request) (*httpclient.Request, error) {
	return &httpclient.Request{APIFormat: t.format.String()}, nil
}

func (t *passthroughOutbound) TransformResponse(ctx context.Context, resp *httpclient.Response) (*llm.Response, error) {
	return &llm.Response{}, nil
}

func (t *passthroughOutbound) TransformStream(ctx context.Context, req *httpclient.Request, stream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*llm.Response], error) {
	return streams.Map(stream, func(ev *httpclient.StreamEvent) *llm.Response {
		return &llm.Response{Model: string(ev.Data)}
	}), nil
}

func (t *passthroughOutbound) TransformError(ctx context.Context, err *httpclient.Error) *llm.ResponseError {
	return nil
}

func (t *passthroughOutbound) AggregateStreamChunks(ctx context.Context, _ *httpclient.Request, chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	return nil, llm.ResponseMeta{}, nil
}

func (t *passthroughPolicyOutbound) AllowPassThroughBody(ctx context.Context, llmReq *llm.Request, providerReq *httpclient.Request) bool {
	return t.allow
}

type passthroughResponsePolicyOutbound struct {
	passthroughOutbound

	allow          bool
	gotLlmReq      *llm.Request
	gotProviderReq *httpclient.Request
}

func (t *passthroughResponsePolicyOutbound) AllowPassThroughResponse(ctx context.Context, llmReq *llm.Request, providerReq *httpclient.Request) bool {
	t.gotLlmReq = llmReq
	t.gotProviderReq = providerReq

	return t.allow
}

// passthroughInbound is an inbound transformer that maps llm responses 1:1 to raw events.
type passthroughInbound struct {
	format llm.APIFormat
}

func (t *passthroughInbound) APIFormat() llm.APIFormat { return t.format }

func (t *passthroughInbound) TransformRequest(ctx context.Context, req *httpclient.Request) (*llm.Request, error) {
	return &llm.Request{APIFormat: t.format}, nil
}

func (t *passthroughInbound) TransformResponse(ctx context.Context, resp *llm.Response) (*httpclient.Response, error) {
	return &httpclient.Response{}, nil
}

func (t *passthroughInbound) TransformStream(ctx context.Context, stream streams.Stream[*llm.Response]) (streams.Stream[*httpclient.StreamEvent], error) {
	return streams.Map(stream, func(llmResp *llm.Response) *httpclient.StreamEvent {
		return &httpclient.StreamEvent{Data: json.RawMessage(llmResp.Model)}
	}), nil
}

func (t *passthroughInbound) TransformError(ctx context.Context, err error) *httpclient.Error {
	return nil
}

func (t *passthroughInbound) AggregateStreamChunks(ctx context.Context, chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	return nil, llm.ResponseMeta{}, nil
}

func TestPassThroughStream_LLMMiddlewareRuns(t *testing.T) {
	ctx := context.Background()
	format := llm.APIFormatOpenAIChatCompletion

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
		Outbound: &passthroughOutbound{format: format},
	}

	state := &PersistenceState{
		CurrentCandidate:      &ChannelModelsCandidate{Channel: channel},
		OriginalRequestStream: lo.ToPtr(true),
		LlmRequest: &llm.Request{
			APIFormat: format,
			Stream:    lo.ToPtr(true),
			RawRequest: &httpclient.Request{
				APIFormat: string(format),
				Body:      []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
			},
		},
		RawProviderRequest: &httpclient.Request{
			APIFormat: string(format),
		},
	}

	outbound := &PersistentOutboundTransformer{
		wrapped: &passthroughOutbound{format: format},
		state:   state,
	}

	tracker := &trackingLLM{}

	rawEvents := []*httpclient.StreamEvent{
		{Data: json.RawMessage(`"evt1"`)},
		{Data: json.RawMessage(`"evt2"`)},
	}
	srcStream := testHTTPStream(rawEvents)

	capMw := captureRawProviderStream(outbound)
	pipelineStream, err := capMw.OnOutboundRawStream(ctx, srcStream)
	require.NoError(t, err)
	require.NotNil(t, state.PassThroughRecorder)

	llmStream, err := outbound.wrapped.TransformStream(ctx, nil, pipelineStream)
	require.NoError(t, err)

	trackedLLM, err := tracker.OnOutboundLlmStream(ctx, llmStream)
	require.NoError(t, err)
	require.True(t, tracker.called, "OnOutboundLlmStream should be called")

	inbound := &passthroughInbound{format: format}
	inboundStream, err := inbound.TransformStream(ctx, trackedLLM)
	require.NoError(t, err)

	applyMw := applyPassThroughStream(outbound)
	result, err := applyMw.OnInboundRawStream(ctx, inboundStream)
	require.NoError(t, err)

	var passthroughEvents []*httpclient.StreamEvent
	for result.Next() {
		passthroughEvents = append(passthroughEvents, result.Current())
	}

	require.Len(t, passthroughEvents, 2)
	assert.Equal(t, rawEvents, passthroughEvents)
	assert.Equal(t, 2, tracker.eventCount(), "tracking middleware should process 2 events")
}

// TestPassThroughResponsesStream_DeepSeekReasoningDoesNotDeadlock verifies that
// provider-specific reasoning events cannot fill the raw pass-through buffer before
// the inbound raw-stream consumer is attached.
func TestPassThroughResponsesStream_DeepSeekReasoningDoesNotDeadlock(t *testing.T) {
	const (
		model               = "deepseek-v4-flash"
		reasoningEventCount = 80
	)

	provider, err := responsestransformer.NewOutboundTransformer("https://api.deepseek.com/v1", "test-api-key")
	require.NoError(t, err)

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "deepseek-responses",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
		Outbound: provider,
	}
	candidate := &ChannelModelsCandidate{
		Channel: channel,
		Models: []biz.ChannelModelEntry{
			{RequestModel: model, ActualModel: model, Source: "direct"},
		},
		APIFormat: string(llm.APIFormatOpenAIResponse),
	}
	state := &PersistenceState{
		OriginalModel:           model,
		ChannelModelsCandidates: []*ChannelModelsCandidate{candidate},
	}
	inbound, outbound := NewPersistentTransformers(state, responsestransformer.NewInboundTransformer())
	executor := &mockExecutor{streamEvents: deepSeekResponsesReasoningEvents(reasoningEventCount)}
	pipe := pipeline.NewFactory(executor).Pipeline(
		inbound,
		outbound,
		pipeline.WithEmptyResponseDetection(),
		pipeline.WithMiddlewares(
			applyPassThroughStream(outbound),
			applyPassThroughRequestBody(outbound, nil),
			captureRawProviderStream(outbound),
		),
	)

	requestBody, err := json.Marshal(map[string]any{
		"model":  model,
		"stream": true,
		"input":  "Reply with OK",
	})
	require.NoError(t, err)

	events := processPassThroughStream(t, pipe.Process, &httpclient.Request{
		Method:      http.MethodPost,
		URL:         "/v1/responses",
		ContentType: "application/json",
		Headers:     http.Header{"Content-Type": []string{"application/json"}},
		Body:        requestBody,
	})

	var eventTypes []string
	reasoningDeltaCount := 0
	for _, event := range events {
		eventTypes = append(eventTypes, event.Type)
		if event.Type == "response.reasoning_text.delta" {
			reasoningDeltaCount++
		}
	}
	require.Equal(t, reasoningEventCount, reasoningDeltaCount)
	require.Contains(t, eventTypes, "response.output_text.delta")
}

// processPassThroughStream runs a streaming pass-through pipeline and drains its
// event stream within a deadline, so a blocked pass-through stream fails the test
// instead of hanging it.
func processPassThroughStream(
	t *testing.T,
	process func(context.Context, *httpclient.Request) (*pipeline.Result, error),
	request *httpclient.Request,
) []*httpclient.StreamEvent {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type streamOutcome struct {
		events []*httpclient.StreamEvent
		err    error
	}

	outcomeCh := make(chan streamOutcome, 1)
	go func() {
		result, err := process(ctx, request)
		if err != nil {
			outcomeCh <- streamOutcome{err: err}
			return
		}
		if !result.Stream {
			outcomeCh <- streamOutcome{err: errors.New("pipeline returned a non-stream result")}
			return
		}

		defer result.EventStream.Close()

		var events []*httpclient.StreamEvent
		for result.EventStream.Next() {
			events = append(events, result.EventStream.Current())
		}

		outcomeCh <- streamOutcome{events: events, err: result.EventStream.Err()}
	}()

	select {
	case outcome := <-outcomeCh:
		require.NoError(t, outcome.err)

		return outcome.events
	case <-time.After(2 * time.Second):
		cancel()

		select {
		case <-outcomeCh:
		case <-time.After(time.Second):
			t.Fatal("pass-through pipeline did not stop after cancellation")
		}

		t.Fatal("pass-through pipeline blocked while streaming raw provider events")

		return nil
	}
}

// TestPassThroughAnthropicStream_RetryPreReadDoesNotDeadlock verifies that a retry
// budget, which makes the pipeline pre-read until the first content event, can consume
// more raw events than the pass-through channel buffers before the inbound raw-stream
// consumer is attached.
func TestPassThroughAnthropicStream_RetryPreReadDoesNotDeadlock(t *testing.T) {
	const (
		model              = "claude-opus-4-7"
		thinkingDeltaCount = 200
	)

	provider, err := anthropic.NewOutboundTransformer("https://api.anthropic.com", "test-api-key")
	require.NoError(t, err)

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "anthropic",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
		Outbound: provider,
	}
	candidate := &ChannelModelsCandidate{
		Channel: channel,
		Models: []biz.ChannelModelEntry{
			{RequestModel: model, ActualModel: model, Source: "direct"},
		},
		APIFormat: string(llm.APIFormatAnthropicMessage),
	}
	state := &PersistenceState{
		OriginalModel:           model,
		ChannelModelsCandidates: []*ChannelModelsCandidate{candidate},
	}
	inbound, outbound := NewPersistentTransformers(state, anthropic.NewInboundTransformer())
	upstreamEvents := anthropicEmptyThinkingEvents(thinkingDeltaCount)
	pipe := pipeline.NewFactory(&mockExecutor{streamEvents: upstreamEvents}).Pipeline(
		inbound,
		outbound,
		pipeline.WithRetry(0, 1, 0),
		pipeline.WithMiddlewares(
			applyPassThroughStream(outbound),
			applyPassThroughRequestBody(outbound, nil),
			captureRawProviderStream(outbound),
		),
	)

	requestBody, err := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 1024,
		"stream":     true,
		"messages":   []map[string]any{{"role": "user", "content": "Reply with OK"}},
	})
	require.NoError(t, err)

	events := processPassThroughStream(t, pipe.Process, &httpclient.Request{
		Method:      http.MethodPost,
		URL:         "/v1/messages",
		ContentType: "application/json",
		Headers:     http.Header{"Content-Type": []string{"application/json"}},
		Body:        requestBody,
	})

	require.Equal(t, upstreamEvents, events)
}

// anthropicEmptyThinkingEvents builds a Claude stream whose thinking block carries no
// text, as sent when the thinking display is omitted: the empty thinking deltas are not
// response content, so only the closing signature_delta commits a pre-read attempt.
func anthropicEmptyThinkingEvents(thinkingDeltaCount int) []*httpclient.StreamEvent {
	event := func(eventType, data string) *httpclient.StreamEvent {
		return &httpclient.StreamEvent{Type: eventType, Data: []byte(data)}
	}

	events := []*httpclient.StreamEvent{
		event("message_start", `{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-opus-4-7","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}`),
		event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`),
	}
	for range thinkingDeltaCount {
		events = append(events, event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":""}}`))
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

// deepSeekResponsesReasoningEvents builds the provider-specific event ordering that
// exposed the pass-through deadlock: many unknown reasoning deltas precede text output.
func deepSeekResponsesReasoningEvents(reasoningCount int) []*httpclient.StreamEvent {
	events := make([]*httpclient.StreamEvent, 0, reasoningCount+3)
	events = append(events, &httpclient.StreamEvent{
		Type: "response.created",
		Data: []byte(`{"type":"response.created","sequence_number":0,"response":{"id":"resp_deepseek_test","object":"response","created_at":1700000000,"status":"in_progress","model":"deepseek-v4-flash","output":[]}}`),
	})

	for i := range reasoningCount {
		events = append(events, &httpclient.StreamEvent{
			Type: "response.reasoning_text.delta",
			Data: fmt.Appendf(
				nil,
				`{"type":"response.reasoning_text.delta","sequence_number":%d,"item_id":"reasoning_1","output_index":0,"content_index":0,"delta":"x"}`,
				i+1,
			),
		})
	}

	events = append(events,
		&httpclient.StreamEvent{
			Type: "response.output_text.delta",
			Data: fmt.Appendf(
				nil,
				`{"type":"response.output_text.delta","sequence_number":%d,"item_id":"message_1","output_index":1,"content_index":0,"delta":"OK"}`,
				reasoningCount+1,
			),
		},
		&httpclient.StreamEvent{
			Type: "response.completed",
			Data: fmt.Appendf(
				nil,
				`{"type":"response.completed","sequence_number":%d,"response":{"id":"resp_deepseek_test","object":"response","created_at":1700000000,"status":"completed","model":"deepseek-v4-flash","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
				reasoningCount+2,
			),
		},
	)

	return events
}

func TestPassThroughStream_ErrorPropagates(t *testing.T) {
	ctx := context.Background()
	format := llm.APIFormatOpenAIChatCompletion

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}

	state := &PersistenceState{
		CurrentCandidate:      &ChannelModelsCandidate{Channel: channel},
		OriginalRequestStream: lo.ToPtr(true),
		LlmRequest: &llm.Request{
			APIFormat: format,
			Stream:    lo.ToPtr(true),
			RawRequest: &httpclient.Request{
				APIFormat: string(format),
				Body:      []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
			},
		},
		RawProviderRequest: &httpclient.Request{
			APIFormat: string(format),
		},
	}
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{apiFormat: format},
		state:   state,
	}

	errTest := errors.New("stream error")
	src := &errorStream{err: errTest}

	capMw := captureRawProviderStream(outbound)
	result, err := capMw.OnOutboundRawStream(ctx, src)
	require.NoError(t, err)

	for result.Next() { //nolint:revive // intentional drain
	}

	assert.Equal(t, errTest, result.Err())
	assert.Equal(t, errTest, state.PassThroughRecorder.Err())
}
