package biz

import (
	"context"
	"slices"

	"github.com/looplj/axonhub/internal/pkg/requestpayload"
)

// OptimizeRequestImages shares bounded project-isolated work across protocol handlers.
// The caller supplies the project from the authorized persisted request.
func (s *RequestService) OptimizeRequestImages(ctx context.Context, projectID, channelID int, body []byte) ([]byte, requestpayload.Optimization) {
	if s.PayloadService != nil && slices.Contains(s.PayloadService.config.CachedImagesOnlyChannels, channelID) {
		return s.imageOptimizer.OptimizeCached(ctx, projectID, body)
	}
	return s.imageOptimizer.Optimize(ctx, projectID, body)
}
