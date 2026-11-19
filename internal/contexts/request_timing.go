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
	bodyRead              time.Time
	firstWrite, delivered atomic.Pointer[time.Time]
	mu                    sync.Mutex
	attempts              []*AttemptClock
}

type AttemptClock struct {
	ID                    int
	ChannelID             int
	Trace                 *httpclient.TransportTrace
	firstContent, settled atomic.Pointer[time.Time]
}

type AttemptTiming struct {
	ExecutionID  int        `json:"execution_id"`
	ChannelID    int        `json:"channel_id"`
	Uploaded     *time.Time `json:"upload_completed_at,omitempty"`
	Headers      *time.Time `json:"upstream_headers_at,omitempty"`
	FirstContent *time.Time `json:"first_model_content_at,omitempty"`
	Settled      *time.Time `json:"budget_settled_at,omitempty"`
}

type RequestTiming struct {
	BodyRead   time.Time       `json:"body_read_at"`
	FirstWrite *time.Time      `json:"gateway_first_write_at,omitempty"`
	Delivered  *time.Time      `json:"gateway_terminal_write_at,omitempty"`
	Attempts   []AttemptTiming `json:"attempts,omitempty"`
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
	a := &AttemptClock{ID: id, ChannelID: channel, Trace: trace}
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
	r := RequestTiming{BodyRead: c.bodyRead, FirstWrite: c.firstWrite.Load(), Delivered: c.delivered.Load()}
	for _, a := range c.attempts {
		upload, headers := a.Trace.ResponseTimes()
		r.Attempts = append(r.Attempts, AttemptTiming{a.ID, a.ChannelID, upload, headers, a.firstContent.Load(), a.settled.Load()})
	}
	return r
}
