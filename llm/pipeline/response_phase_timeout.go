package pipeline

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

var ErrRequestUploadTimeout = errors.New("upstream request upload timeout")

// responsePhaseGuard keeps native HTTP upload and response budgets separate.
// Executors without HTTP progress retain their previous response timeout behavior.
// Every phase still inherits the caller's overall deadline and cancellation.
type responsePhaseGuard struct {
	mu              sync.Mutex
	parent          context.Context
	request         *httpclient.Request
	cancel          context.CancelCauseFunc
	timer           *time.Timer
	generation      uint64
	done            bool
	failure         error
	phase           httpclient.TransportPhase
	responseTimeout time.Duration
	uploadTimeout   time.Duration
	responseCause   error
}

func newResponsePhaseGuard(ctx context.Context, request *httpclient.Request, responseTimeout, uploadTimeout time.Duration, cause error) (context.Context, *responsePhaseGuard) {
	if uploadTimeout < 0 {
		uploadTimeout = responseTimeout
	}
	child, cancel := context.WithCancelCause(ctx)
	g := &responsePhaseGuard{parent: ctx, request: request, cancel: cancel, responseTimeout: responseTimeout, uploadTimeout: uploadTimeout, responseCause: cause}
	g.arm(responseTimeout, cause)
	previous := request.OnHTTPProgress
	request.OnHTTPProgress = func(phase httpclient.TransportPhase, at time.Time) {
		g.progress(phase)
		if previous != nil {
			previous(phase, at)
		}
	}
	return child, g
}

func (g *responsePhaseGuard) arm(timeout time.Duration, cause error) {
	g.generation++
	if g.timer != nil {
		g.timer.Stop()
	}
	if timeout <= 0 {
		return
	}
	generation := g.generation
	g.timer = time.AfterFunc(timeout, func() {
		g.mu.Lock()
		if g.done || g.generation != generation || g.parent.Err() != nil {
			g.mu.Unlock()
			return
		}
		g.failure, g.done = cause, true
		g.mu.Unlock()
		g.cancel(cause)
	})
}

func (g *responsePhaseGuard) progress(phase httpclient.TransportPhase) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.done {
		return
	}
	switch phase {
	case httpclient.TransportStarted:
		if g.phase != "" {
			return
		}
		g.phase = phase
		g.arm(g.uploadTimeout, ErrRequestUploadTimeout)
	case httpclient.TransportUploaded, httpclient.TransportHeaders:
		if g.phase != httpclient.TransportStarted {
			return
		}
		// An early HTTP response (for example a 413) also ends the upload phase.
		g.phase = phase
		g.arm(g.responseTimeout, g.responseCause)
	}
}

func (g *responsePhaseGuard) timeoutError() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.failure
}

func (g *responsePhaseGuard) timedOut() bool { return g.timeoutError() != nil }

func (g *responsePhaseGuard) stop() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.done = true
	g.generation++
	if g.timer != nil {
		g.timer.Stop()
	}
}

func (g *responsePhaseGuard) cancelStream() {
	if g != nil {
		g.stop()
		g.cancel(nil)
	}
}

func (g *responsePhaseGuard) completeFirstEventPhase() { g.stop() }

func (g *responsePhaseGuard) acceptFirstEvent() bool {
	g.stop()
	if g.timedOut() {
		return false
	}
	g.request.ReportHTTPProgress(httpclient.TransportFirstEvent, time.Now())
	return true
}

func (g *responsePhaseGuard) finishBeforeFirstEvent(err error) error {
	if g == nil {
		return err
	}
	g.stop()
	if failure := g.timeoutError(); failure != nil {
		err = failure
	} else {
		err = requestTimeoutCause(g.parent, err)
	}
	g.cancel(nil)
	return err
}

func requestTimeoutCause(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); err != nil && isResponseTimeoutError(cause) {
		return cause
	}
	return err
}

// Normalize transport cancellation before persistence and other stream observers
// see it, including a non-stream timeout inherited by an auto-aggregated stream.
type responseTimeoutStream struct {
	streams.Stream[*httpclient.StreamEvent]
	ctx context.Context
}

func (s *responseTimeoutStream) Err() error {
	return requestTimeoutCause(s.ctx, s.Stream.Err())
}
