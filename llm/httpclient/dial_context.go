package httpclient

import (
	"context"
	"net"
	"sync/atomic"
	"time"
)

// A dial timeout or DNS worker may outlive DialContext. During the dial it
// needs the caller's trace hooks and context values. Once it returns, those
// hooks must not keep a completed gateway request and its body alive.
type dialParent struct {
	source      atomic.Pointer[context.Context]
	done        <-chan struct{}
	deadline    time.Time
	hasDeadline bool
}

func (p *dialParent) Deadline() (time.Time, bool) { return p.deadline, p.hasDeadline }
func (p *dialParent) Done() <-chan struct{}       { return p.done }

func (p *dialParent) Err() error {
	if src := p.source.Load(); src != nil {
		return (*src).Err()
	}
	// A custom parent's cancellation propagation can finish concurrently
	// with dial cleanup. A closed Done channel must still report an error.
	select {
	case <-p.done:
		return context.Canceled
	default:
		return nil
	}
}

func (p *dialParent) Value(key any) any {
	if src := p.source.Load(); src != nil {
		return (*src).Value(key)
	}
	return nil
}

func dialWithScopedValues(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), network, address string) (net.Conn, error) {
	deadline, hasDeadline := ctx.Deadline()
	parent := &dialParent{done: ctx.Done(), deadline: deadline, hasDeadline: hasDeadline}
	parent.source.Store(&ctx)
	// The dialer sees a normal cancelable child with a stable Done channel
	// and cancellation cause, even if a resolver still observes it on return.
	child, cancel := context.WithCancel(parent)
	defer func() {
		cancel()
		parent.source.Store(nil)
	}()
	return dial(child, network, address)
}
