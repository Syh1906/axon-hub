package datatransfer

import (
	"context"
	"io"

	"github.com/looplj/axonhub/internal/log"
)

// Wait for a started cancellation callback before its HTTP handler returns.
// Gin may reuse its response writer immediately after that return.
func closeOnCancellation(ctx context.Context, closer io.Closer) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				log.Error(ctx, "Migration stream cancellation panicked")
			}
		}()
		_ = closer.Close()
	})
	return func() {
		if !stop() {
			<-done
		}
	}
}
