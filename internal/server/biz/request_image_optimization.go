package biz

import (
	"context"

	"github.com/looplj/axonhub/internal/pkg/requestpayload"
)

// OptimizeRequestImages shares bounded project-isolated work across protocol handlers.
// The caller supplies the project from the authorized persisted request.
func (s *RequestService) OptimizeRequestImages(ctx context.Context, projectID int, body []byte) ([]byte, requestpayload.Optimization) {
	return s.imageOptimizer.Optimize(ctx, projectID, body)
}
