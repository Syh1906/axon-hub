package biz

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/looplj/axonhub/internal/log"
)

type consumptionFlight struct {
	mu    sync.Mutex
	calls map[string]*consumptionCall
}

type consumptionCall struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	value   *ConsumptionResult
	err     error
}

func (g *consumptionFlight) do(ctx context.Context, key string, compute func(context.Context) (*ConsumptionResult, error)) (*ConsumptionResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*consumptionCall)
	}
	c := g.calls[key]
	if c == nil {
		shared, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		c = &consumptionCall{done: make(chan struct{}), cancel: cancel}
		g.calls[key] = c
		go func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					log.Error(shared, "consumption query panicked", log.Any("panic", recovered))
					c.err = fmt.Errorf("compute consumption: %v", recovered)
				}
				cancel()
				g.mu.Lock()
				if g.calls[key] == c {
					delete(g.calls, key)
				}
				close(c.done)
				g.mu.Unlock()
			}()
			c.value, c.err = compute(shared)
		}()
	}
	c.waiters++
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		c.waiters--
		if c.waiters == 0 {
			if g.calls[key] == c {
				delete(g.calls, key)
			}
			c.cancel()
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return c.value, c.err
	}
}
