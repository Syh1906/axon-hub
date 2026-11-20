package orchestrator

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

type responsesCompletion struct {
	output       responses.HistoryOutput
	mu           sync.Mutex
	body         []byte
	bytes        int
	overflow     bool
	budget       *shared.ResponsesBudget
	retained     int
	metadataOnly bool
}

func (s *responsesCompletion) observe(event *httpclient.StreamEvent) {
	if event == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bytes += len(event.Data)
	if s.bytes > responsesSessionMaxResponse {
		s.overflow = true
	}
	kind := gjson.GetBytes(event.Data, "type").String()
	if !s.overflow && !s.metadataOnly {
		s.output.Observe(kind, event.Data, func(size int) bool {
			if s.budget != nil {
				if !s.budget.Replace(s.retained, s.retained+size) {
					return false
				}
				s.retained += size
			}
			return true
		})
	}
	if kind == "response.completed" || kind == "response.done" {
		body := gjson.GetBytes(event.Data, "response")
		if body.Get("status").String() == "completed" && body.Get("id").String() != "" {
			var completed []byte
			if !s.overflow && !s.metadataOnly {
				var ok bool
				completed, ok = s.output.Complete([]byte(body.Raw))
				if !ok {
					s.metadataOnly = true
				}
			}
			s.output = responses.HistoryOutput{}
			if !s.overflow && !s.metadataOnly && s.budget != nil {
				if s.budget.Replace(s.retained, len(completed)) {
					s.retained = len(completed)
				} else {
					s.overflow = true
				}
			}
			if s.overflow || s.metadataOnly {
				s.body, _ = json.Marshal(struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				}{body.Get("id").String(), "completed"})
			} else {
				s.body = completed
			}
		}
	}
}

type responsesProviderStream struct {
	streams.Stream[*httpclient.StreamEvent]

	completion *responsesCompletion
	current    *httpclient.StreamEvent
}

func (s *responsesProviderStream) Next() bool {
	if !s.Stream.Next() {
		s.current = nil
		return false
	}
	s.current = s.Stream.Current()
	s.completion.observe(s.current)
	return true
}

func (s *responsesProviderStream) Current() *httpclient.StreamEvent { return s.current }

func captureResponsesProvider(p *PersistentOutboundTransformer) pipeline.Middleware {
	return &responsesProviderCapture{state: p.state}
}

type responsesProviderCapture struct {
	pipeline.DummyMiddleware

	state *PersistenceState
}

func (m *responsesProviderCapture) Name() string { return "responses_provider_completion" }

func (m *responsesProviderCapture) OnOutboundRawResponse(ctx context.Context, response *httpclient.Response) (*httpclient.Response, error) {
	if c := m.state.ResponsesContinuation; c != nil {
		c.capture.mu.Lock()
		c.capture.body = response.Body
		c.capture.overflow = len(response.Body) > responsesSessionMaxResponse
		if !c.capture.overflow && c.capture.budget != nil {
			if c.capture.budget.Replace(0, len(response.Body)) {
				c.capture.retained = len(response.Body)
			} else {
				c.capture.overflow = true
			}
		}
		c.capture.mu.Unlock()
	}
	return response, nil
}

func (m *responsesProviderCapture) OnOutboundRawStream(ctx context.Context, stream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*httpclient.StreamEvent], error) {
	if c := m.state.ResponsesContinuation; c != nil {
		return httpclient.WithResponseHeaders(&responsesProviderStream{Stream: stream, completion: c.capture}, httpclient.GetResponseHeaders(stream)), nil
	}
	return stream, nil
}

func (c *responsesContinuation) accept(ctx context.Context) {
	c.capture.mu.Lock()
	defer c.capture.mu.Unlock()
	body := c.capture.body
	if gjson.GetBytes(body, "status").String() != "completed" {
		return
	}
	c.remember(ctx, body)
}

// remember is called with capture.mu held and only with a completed response or
// the API's intentional interruption containing exclusively completed items.
func (c *responsesContinuation) remember(ctx context.Context, body []byte) {
	if len(body) == 0 || !json.Valid(body) {
		return
	}
	id := gjson.GetBytes(body, "id").String()
	if id == "" {
		return
	}
	origin := shared.ResponsesOrigin{}
	if attempt := shared.GetResponsesAttempt(ctx); attempt != nil {
		origin = attempt.Origin()
	}
	if lane := shared.GetResponsesLane(ctx); lane != nil {
		c.lane = lane
		record := shared.ResponsesRecord{ID: id, Origin: origin}
		if c.retain && !c.capture.overflow && !c.capture.metadataOnly {
			retained := c.capture.retained == 0 || c.capture.retained == len(body)
			if c.capture.retained == len(body) {
				c.retentionBytes += c.capture.retained
				c.capture.retained = 0
			}
			var response struct {
				Output []json.RawMessage `json:"output"`
			}
			if retained && json.Unmarshal(body, &response) == nil {
				if c.replaceInputAtCommit {
					c.input = responseSessionInputItems([]byte(gjson.GetBytes(c.effective, "input").Raw))
				}
				record.Snapshot = shared.NewResponsesSnapshot(c.parent, c.input, response.Output)
			}
		}
		reserved := c.retentionBytes + c.capture.retained
		c.retentionBytes, c.capture.retained = 0, 0
		lane.RememberReserved(record, reserved)
	} else if c.store != nil {
		requestBody := c.effective
		if c.complete {
			requestBody = c.snapshotBody
		}
		if c.capture.overflow || c.capture.metadataOnly {
			requestBody = []byte(`{"previous_response_id":"unavailable"}`)
		}
		if c.holdHTTPHistory && !c.capture.overflow && !c.capture.metadataOnly {
			c.pendingHTTPHistory = &responsesHTTPHistory{input: requestBody, output: body}
		} else {
			c.store.record(ctx, requestBody, body)
		}
		if c.capture.budget != nil {
			c.capture.budget.Replace(c.capture.retained, 0)
			c.capture.retained = 0
		}
	}
	c.capture.body = nil
}

func (c *responsesContinuation) acceptInterrupted(ctx context.Context, body []byte) {
	c.capture.mu.Lock()
	defer c.capture.mu.Unlock()
	if gjson.GetBytes(body, "incomplete_details.reason").String() != "interrupted" {
		return
	}
	// The synthetic response is built from completed downstream items, not from
	// an unfinished provider terminal. Do not reuse its closed upstream ID.
	if attempt := shared.GetResponsesAttempt(ctx); attempt != nil {
		origin := attempt.Origin()
		origin.Generation = 0
		attempt.SetOrigin(origin)
	}
	if c.capture.budget != nil {
		c.capture.budget.Replace(c.capture.retained, 0)
		c.capture.retained = 0
	}
	c.capture.overflow = len(body) > responsesSessionMaxResponse
	c.remember(ctx, body)
}

func (c *responsesContinuation) releaseCapture() {
	c.capture.mu.Lock()
	defer c.capture.mu.Unlock()
	if c.capture.budget != nil {
		c.capture.budget.Replace(c.capture.retained, 0)
	}
	c.capture.retained = 0
	c.capture.body = nil
}

type responsesAcceptedStream struct {
	streams.Stream[*httpclient.StreamEvent]

	ctx          context.Context
	continuation *responsesContinuation
	current      *httpclient.StreamEvent
}

func (s *responsesAcceptedStream) Next() bool {
	if !s.Stream.Next() {
		s.current = nil
		return false
	}
	s.current = s.Stream.Current()
	if classifyStreamTerminalEvent(s.current) == streamTerminalCompleted {
		s.continuation.accept(s.ctx)
	}
	return true
}

func (s *responsesAcceptedStream) Current() *httpclient.StreamEvent { return s.current }

func (s *responsesAcceptedStream) Close() error {
	defer s.continuation.releaseRetention()
	defer s.continuation.releaseCapture()
	defer s.continuation.flushHTTPHistory(s.ctx)
	return s.Stream.Close()
}
