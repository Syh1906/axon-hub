package biz

import (
	"context"
	"math/bits"
	"slices"
	"sync"
	"time"

	"github.com/looplj/axonhub/internal/pkg/requestpayload"
	"github.com/looplj/axonhub/llm/httpclient"
)

// OptimizeRequestImages shares bounded project-isolated work across protocol handlers.
// The caller supplies the project from the authorized persisted request.
func (s *RequestService) OptimizeRequestImages(ctx context.Context, projectID, channelID int, body []byte, upload func() httpclient.UploadEstimate) ([]byte, requestpayload.Optimization) {
	cachedOnly := s.PayloadService != nil && slices.Contains(s.PayloadService.config.CachedImagesOnlyChannels, channelID)
	if cachedOnly && !s.imageOptimizer.HasCachedImages() {
		return s.imageOptimizer.OptimizeCached(ctx, projectID, body)
	}
	if cache, _ := ctx.Value(bodyInspectionKey{}).(*bodyInspection); cache != nil && len(body) >= requestpayload.MinImageBytes {
		start := time.Now()
		cache.mu.Lock()
		cache.inspect(body)
		analysis := cache.analysis
		cache.mu.Unlock()
		optimize := func(onlyCached bool) ([]byte, requestpayload.Optimization) {
			out, stats := s.imageOptimizer.OptimizeAnalyzed(ctx, projectID, body, analysis, onlyCached)
			stats.DurationMs = time.Since(start).Milliseconds()
			return out, stats
		}
		if cachedOnly || s.PayloadService == nil || !analysis.HasPNG() {
			return optimize(cachedOnly)
		}
		estimate := upload()
		key := imageWorkClass{project: projectID, channel: channelID, size: bits.Len(uint(len(body))), tool: analysis.Images.ToolCount > 0, resource: estimate.Resource}
		h := &s.PayloadService.images
		if estimate.PreferZstd && !h.favorsPNG(key, len(body), estimate, time.Now()) {
			return optimize(true)
		}
		if h.cooling(key, estimate.NSPerByte, time.Now()) {
			return optimize(true)
		}
		out, stats := optimize(false)
		if ctx.Err() == nil && stats.BusyImages == 0 {
			h.record(key, stats, time.Now())
		}
		return out, stats
	}
	if cachedOnly {
		return s.imageOptimizer.OptimizeCached(ctx, projectID, body)
	}
	return s.imageOptimizer.Optimize(ctx, projectID, body)
}

type imageWorkClass struct {
	project, channel, size int
	tool                   bool
	resource               [32]byte
}

type imageWorkResult struct {
	misses int
	until  time.Time
	saved  int
	cpu    time.Duration
	tried  time.Time
}

type imageWorkHistory struct {
	mu   sync.Mutex
	rows map[imageWorkClass]imageWorkResult
}

func (h *imageWorkHistory) favorsPNG(k imageWorkClass, size int, e httpclient.UploadEstimate, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.rows[k]
	return r.saved > 0 && now.Sub(r.tried) < time.Minute && e.ZstdSavingsNS > 0 &&
		float64(min(r.saved, size))*e.NSPerByte-float64(r.cpu) > e.ZstdSavingsNS
}

func (h *imageWorkHistory) cooling(k imageWorkClass, nsPerByte float64, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.rows[k]
	return now.Before(r.until) || nsPerByte > 0 && r.saved > 0 && now.Sub(r.tried) < time.Minute && float64(r.saved)*nsPerByte <= float64(r.cpu)
}

func (h *imageWorkHistory) record(k imageWorkClass, s requestpayload.Optimization, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s.DurationMs == 0 {
		return
	}
	prev, ok := h.rows[k]
	if !ok && len(h.rows) >= 256 {
		for key := range h.rows {
			delete(h.rows, key)
			break
		}
	}
	if h.rows == nil {
		h.rows = make(map[imageWorkClass]imageWorkResult)
	}
	if s.OutputBytes < s.InputBytes {
		if s.ColdEncodes > 0 && s.CacheHits == 0 {
			h.rows[k] = imageWorkResult{saved: s.InputBytes - s.OutputBytes, cpu: time.Duration(s.DurationMs) * time.Millisecond, tried: now}
		}
		return
	}
	if !prev.until.IsZero() && !now.Before(prev.until) {
		prev = imageWorkResult{misses: 2}
	}
	prev.misses++
	if prev.misses >= 3 {
		prev.until = now.Add(time.Minute)
	}
	h.rows[k] = prev
}
