package contexts

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/looplj/axonhub/llm/httpclient"
)

type requestClockKey struct{}

// RequestClock follows one inbound HTTP request or one WS generation frame.
// Each upstream attempt retains its own clock, including competing title attempts.
type RequestClock struct {
	answerDelivery        answerDelivery
	bodyRead              time.Time
	firstWriteDelta       atomic.Pointer[TimedStreamEvent]
	firstWriteContent     atomic.Pointer[TimedStreamEvent]
	firstAnswer           atomic.Pointer[time.Time]
	firstWrite, delivered atomic.Pointer[time.Time]
	mu                    sync.Mutex
	attempts              []*AttemptClock
	onFinish              []func(RequestTiming, bool)
}

type AttemptClock struct {
	ID                     int
	ChannelID              int
	Trace                  *httpclient.TransportTrace
	firstContent, settled  atomic.Pointer[time.Time]
	firstEvent, firstDelta atomic.Pointer[TimedStreamEvent]
	semantic               atomic.Pointer[TimedStreamEvent]
	protocolOutput         atomic.Pointer[TimedStreamEvent]
	prepared               time.Time
	dispatched             atomic.Pointer[time.Time]
}

// TimedStreamEvent records the time, protocol category and byte count of an event.
type TimedStreamEvent struct {
	At    time.Time `json:"at"`
	Type  string    `json:"type"`
	Bytes int       `json:"event_bytes"`
}

type AttemptTiming struct {
	Semantic     *TimedStreamEvent `json:"first_output_progress,omitempty"`
	FirstEvent   *TimedStreamEvent `json:"first_responses_event,omitempty"`
	FirstDelta   *TimedStreamEvent `json:"first_responses_delta,omitempty"`
	ExecutionID  int               `json:"execution_id"`
	ChannelID    int               `json:"channel_id"`
	Prepared     time.Time         `json:"attempt_prepared_at,omitzero"`
	Dispatched   *time.Time        `json:"upstream_dispatch_at,omitempty"`
	Uploaded     *time.Time        `json:"upload_completed_at,omitempty"`
	Headers      *time.Time        `json:"upstream_headers_at,omitempty"`
	FirstContent *time.Time        `json:"first_model_content_at,omitempty"`
	Settled      *time.Time        `json:"budget_settled_at,omitempty"`
}

type RequestTiming struct {
	SingleAnswerBatch     bool              `json:"single_answer_batch,omitempty"`
	AnswerTokensPerSecond *float64          `json:"answer_tokens_per_second,omitempty"`
	FirstAnswer           *time.Time        `json:"first_answer_write_at,omitempty"`
	FirstWriteDelta       *TimedStreamEvent `json:"gateway_first_responses_delta_write,omitempty"`
	FirstWriteContent     *TimedStreamEvent `json:"gateway_first_content_write,omitempty"`
	BodyRead              time.Time         `json:"body_read_at"`
	FirstWrite            *time.Time        `json:"gateway_first_write_at,omitempty"`
	Delivered             *time.Time        `json:"gateway_terminal_write_at,omitempty"`
	Attempts              []AttemptTiming   `json:"attempts,omitempty"`
}

func WithRequestClock(ctx context.Context, bodyRead time.Time) (context.Context, *RequestClock) {
	if bodyRead.IsZero() {
		bodyRead = time.Now()
	}
	clock := &RequestClock{bodyRead: bodyRead}
	return context.WithValue(ctx, requestClockKey{}, clock), clock
}

func GetRequestClock(ctx context.Context) *RequestClock {
	clock, _ := ctx.Value(requestClockKey{}).(*RequestClock)
	return clock
}

func (c *RequestClock) AddAttempt(id, channel int, trace *httpclient.TransportTrace) *AttemptClock {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	a := &AttemptClock{ID: id, ChannelID: channel, Trace: trace, prepared: time.Now()}
	c.attempts = append(c.attempts, a)
	return a
}

func markClock(v *atomic.Pointer[time.Time]) {
	if v.Load() == nil {
		at := time.Now()
		v.CompareAndSwap(nil, &at)
	}
}

func (a *AttemptClock) FirstContent() {
	if a != nil {
		markClock(&a.firstContent)
	}
}

// Dispatch marks entry to the upstream executor after admission.
func (a *AttemptClock) Dispatch() {
	if a != nil {
		markClock(&a.dispatched)
	}
}

// OnFinish registers a request-scoped observation without extending stream lifetime.
func (c *RequestClock) OnFinish(f func(RequestTiming, bool)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onFinish = append(c.onFinish, f)
}

// Finish runs optional observers outside the clock lock after writer finalization.
func (c *RequestClock) Finish(failed bool) {
	c.mu.Lock()
	observers := c.onFinish
	c.onFinish = nil
	c.mu.Unlock()
	if len(observers) != 0 {
		snapshot := c.Snapshot()
		for _, f := range observers {
			f(snapshot, failed)
		}
	}
}

func (c *RequestClock) Wrote(terminal bool) {
	if c == nil {
		return
	}
	markClock(&c.firstWrite)
	if terminal {
		markClock(&c.delivered)
	}
}

func (c *RequestClock) Settled(id int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range c.attempts {
		if a.ID == id {
			markClock(&a.settled)
			return
		}
	}
}

func (c *RequestClock) ReportAtInfo(failed bool) bool {
	return failed || c.delivered.Load() == nil || time.Since(c.bodyRead) >= 3*time.Second
}

func (c *RequestClock) Snapshot() RequestTiming {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := RequestTiming{BodyRead: c.bodyRead, FirstWrite: c.firstWrite.Load(), FirstWriteDelta: c.firstWriteDelta.Load(), Delivered: c.delivered.Load()}
	r.FirstWriteContent = c.firstWriteContent.Load()
	r.FirstAnswer = c.firstAnswer.Load()
	r.SingleAnswerBatch, r.AnswerTokensPerSecond = c.answerDelivery.result()
	for _, a := range c.attempts {
		upload, headers := a.Trace.ResponseTimes()
		r.Attempts = append(r.Attempts, AttemptTiming{
			ExecutionID: a.ID, ChannelID: a.ChannelID, Uploaded: upload, Headers: headers,
			FirstContent: a.firstContent.Load(), Settled: a.settled.Load(),
			FirstEvent: a.firstEvent.Load(), FirstDelta: a.firstDelta.Load(),
			Semantic: a.semantic.Load(),
			Prepared: a.prepared, Dispatched: a.dispatched.Load(),
		})
	}
	return r
}
