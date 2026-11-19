package orchestrator

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/pkg/xcontext"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

// Recovery uses the input that survived channel and plugin rewrites paired with
// the output visible to the client. Preserve provider and client IDs when different.
func (c *responsesContinuation) recordDelivered(ctx context.Context, store *responsesSessionStore, prepared, delivered []byte) {
	if pending := c.takeHTTPHistory(); pending != nil {
		providerID := gjson.GetBytes(pending.output, "id").String()
		if gjson.GetBytes(delivered, "id").String() != providerID || gjson.GetBytes(delivered, "status").String() != "completed" {
			store.record(ctx, pending.input, pending.output)
		}
		prepared = pending.input
	}
	store.record(ctx, prepared, delivered)
}

type responsesHTTPHistory struct{ input, output []byte }

func (c *responsesContinuation) takeHTTPHistory() *responsesHTTPHistory {
	if c == nil || !c.holdHTTPHistory {
		return nil
	}
	c.capture.mu.Lock()
	defer c.capture.mu.Unlock()
	pending := c.pendingHTTPHistory
	c.pendingHTTPHistory = nil
	return pending
}

func (c *responsesContinuation) flushHTTPHistory(ctx context.Context) {
	if pending := c.takeHTTPHistory(); pending != nil {
		c.store.continuationRecorder(ctx, pending.input)(pending.output)
	}
}

func responsesOriginRecorder(ctx context.Context, state *PersistenceState, requestBody []byte) func([]byte) error {
	return func(body []byte) error {
		persistCtx, cancel := xcontext.DetachWithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := state.RequestService.RecordResponsesOrigin(persistCtx, state.RequestExec, requestBody, body); err != nil {
			log.Warn(persistCtx, "Unable to retain optional Responses output origin", log.Cause(err))
		}
		return nil
	}
}

func responsesAffinityRecorder(ctx context.Context, state *PersistenceState, requestBody []byte) func([]byte) {
	return func(body []byte) {
		if !state.publishesRequest() {
			return
		}
		if state.StreamReturned {
			if !state.StreamCompleted || !state.DeliveryAcknowledged {
				return
			}
		} else if ctx.Err() != nil {
			return
		}
		persistCtx, cancel := xcontext.DetachWithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := state.RequestService.RecordResponsesAffinity(persistCtx, state.RequestExec, requestBody, body); err != nil {
			log.Warn(persistCtx, "Unable to retain optional Responses affinity", log.Cause(err))
		}
	}
}

type responsesOriginStream struct {
	streams.Stream[*httpclient.StreamEvent]

	record    func([]byte) error
	current   *httpclient.StreamEvent
	err       error
	affinity  func([]byte)
	completed []byte
	closeOnce sync.Once
	closeErr  error
}

func (s *responsesOriginStream) Next() bool {
	s.current = nil
	if s.err != nil || !s.Stream.Next() {
		return false
	}
	event := s.Stream.Current()
	if event == nil {
		return true
	}
	kind := gjson.GetBytes(event.Data, "type").String()
	if kind == "" {
		kind = event.Type
	}
	switch kind {
	case "response.output_item.done":
		item := gjson.GetBytes(event.Data, "item")
		if item.Get("encrypted_content").String() != "" {
			// Persist before the consumer receives this completed item, including
			// when the following token/tool generation is canceled or fails.
			body, _ := json.Marshal(struct {
				Status string            `json:"status"`
				Output []json.RawMessage `json:"output"`
			}{"incomplete", []json.RawMessage{json.RawMessage(item.Raw)}})
			s.err = s.record(body)
		}
	case "response.completed", "response.done", "response.incomplete":
		response := gjson.GetBytes(event.Data, "response")
		if response.IsObject() {
			body := []byte(response.Raw)
			s.err = s.record(body)
			if kind != "response.incomplete" {
				s.completed = body
			}
		}
	}
	if s.err != nil {
		return false
	}
	s.current = event
	return true
}

func (s *responsesOriginStream) Current() *httpclient.StreamEvent { return s.current }

func (s *responsesOriginStream) Err() error {
	if s.err != nil {
		return s.err
	}
	return s.Stream.Err()
}

func (s *responsesOriginStream) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.Stream.Close()
		if s.closeErr == nil && s.affinity != nil && s.completed != nil {
			s.affinity(s.completed)
		}
	})
	return s.closeErr
}

func (processor *ChatCompletionOrchestrator) prepareResponseDelivery(ctx context.Context, state *PersistenceState, request *httpclient.Request, preparedResponsesBody []byte, result *pipeline.Result, continuation *responsesContinuation) (ChatCompletionResult, error) {
	requestID := 0
	if state.Request != nil {
		requestID = state.Request.ID
	}
	// Return result based on stream type
	var recordOrigin func([]byte) error
	var recordAffinity func([]byte)
	if state.LlmRequest != nil && isResponsesFormat(state.LlmRequest.APIFormat) {
		recordOrigin = responsesOriginRecorder(ctx, state, request.Body)
		recordAffinity = responsesAffinityRecorder(ctx, state, request.Body)
	}
	if result.Stream {
		state.StreamReturned = true
		if recordOrigin != nil {
			result.EventStream = &responsesOriginStream{Stream: result.EventStream, record: recordOrigin, affinity: recordAffinity}
		}
		var retainContinuation func([]byte) error
		if preparedResponsesBody != nil {
			// HTTP/restart history retains its existing persistence contract.
			result.EventStream = processor.responsesSessions.wrapStream(ctx, preparedResponsesBody, result.EventStream, continuation)
		}
		if shared.IsResponsesWebSocket(ctx) && continuation != nil {
			retainContinuation = func(body []byte) error {
				continuation.acceptInterrupted(ctx, body)
				if recordOrigin != nil {
					if err := recordOrigin(body); err != nil {
						return err
					}
				}
				// Persist the actual turn without flattening native history. The
				// connection-local snapshot is independent of payload storage.
				if processor.responsesSessions != nil {
					processor.responsesSessions.continuationRecorder(ctx, continuation.effective)(body)
				}
				return nil
			}
		}
		return ChatCompletionResult{
			RequestID:                   requestID,
			ChatCompletion:              nil,
			ChatCompletionStream:        result.EventStream,
			RetainResponsesContinuation: retainContinuation,
		}, nil
	}
	if recordOrigin != nil && result.Response != nil {
		if err := recordOrigin(result.Response.Body); err != nil {
			return ChatCompletionResult{RequestID: requestID}, err
		}
		recordAffinity(result.Response.Body)
	}
	if preparedResponsesBody != nil && result.Response != nil {
		continuation.recordDelivered(ctx, processor.responsesSessions, preparedResponsesBody, result.Response.Body)
	}

	return ChatCompletionResult{
		RequestID:            requestID,
		ChatCompletion:       result.Response,
		ChatCompletionStream: nil,
	}, nil
}
