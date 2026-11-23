package xcontext

import (
	"context"
	"time"
)

func DetachWithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	deadline, cancel := context.WithTimeout(context.Background(), timeout)
	return detachedTimeout{Context: deadline, values: ctx}, cancel
}

// Keep the deadline's timer independent of request data. A stopped timer may
// still be reachable from the runtime until it next services the timer heap.
type detachedTimeout struct {
	context.Context
	values context.Context
}

func (c detachedTimeout) Value(key any) any {
	// The deadline context owns cancellation, including context.Cause and
	// propagation to children. All application values come from the request.
	if value := c.Context.Value(key); value != nil {
		return value
	}
	return c.values.Value(key)
}
