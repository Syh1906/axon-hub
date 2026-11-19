package requestpayload

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/looplj/axonhub/llm/encodingbudget"
)

const (
	// MinImageBytes is the minimum inline size considered for PNG optimization.
	MinImageBytes           = 64 << 10
	maxImageBytes           = 16 << 20
	maxCacheBytes           = 64 << 20
	maxCacheEntries         = 512
	cacheTTL                = 5 * time.Minute
	compressionRetryDelay   = 30 * time.Second
	maxOptimizationDuration = time.Second
)

type cacheKey struct {
	project int
	digest  [32]byte
}
type cacheEntry struct {
	key     cacheKey
	data    string
	expires time.Time
}

// Optimizer is shared by RequestService. Its zero value is usable; do not copy it.
// Cache contents never go to disk or another project. Only known PNG inputs change.
type Optimizer struct {
	once    sync.Once
	mu      sync.Mutex
	entries map[cacheKey]*list.Element
	lru     list.List
	bytes   int
}

type Optimization struct {
	CachedOnly      bool  `json:"cached_only,omitempty"`
	InputBytes      int   `json:"input_bytes"`
	OutputBytes     int   `json:"output_bytes"`
	OptimizedImages int   `json:"optimized_images"`
	CacheHits       int   `json:"cache_hits"`
	ColdEncodes     int   `json:"cold_encodes,omitempty"`
	BusyImages      int   `json:"busy_images,omitempty"`
	PreservedImages int   `json:"preserved_images,omitempty"`
	BudgetExhausted bool  `json:"budget_exhausted,omitempty"`
	DurationMs      int64 `json:"duration_ms"`
}

func (o *Optimizer) Optimize(ctx context.Context, project int, body []byte) ([]byte, Optimization) {
	return o.optimize(ctx, project, body, nil, false)
}

// OptimizeCached only reuses previously compressed PNGs from this project.
func (o *Optimizer) OptimizeCached(ctx context.Context, project int, body []byte) ([]byte, Optimization) {
	return o.optimize(ctx, project, body, nil, true)
}

func (o *Optimizer) HasCachedImages() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.bytes != 0
}

// OptimizeAnalyzed reuses the PNG positions recorded for this exact body version.
func (o *Optimizer) OptimizeAnalyzed(ctx context.Context, project int, body []byte, a *Analysis, cachedOnly bool) ([]byte, Optimization) {
	if !bytes.Equal(a.body, body) {
		a = nil
	}
	return o.optimize(ctx, project, body, a, cachedOnly)
}

func (o *Optimizer) optimize(ctx context.Context, project int, body []byte, a *Analysis, cachedOnly bool) ([]byte, Optimization) {
	start := time.Now()
	stats := Optimization{InputBytes: len(body), OutputBytes: len(body), CachedOnly: cachedOnly}
	if len(body) < MinImageBytes {
		return body, stats
	}
	o.once.Do(func() { o.entries = make(map[cacheKey]*list.Element) })
	if cachedOnly && !o.HasCachedImages() {
		return body, stats
	}
	workCtx, stop := context.WithTimeout(ctx, maxOptimizationDuration)
	defer stop()
	type replacement struct {
		start, end int
		data       []byte
	}
	var changes []replacement
	visit := func(part pngLocation) {
		if workCtx.Err() != nil {
			stats.BudgetExhausted = true
			stats.PreservedImages++
			return
		}
		input := part.data(body)
		key := cacheKey{project: project, digest: sha256.Sum256(input)}
		data, hit := o.get(key, time.Now())
		if hit {
			stats.CacheHits++
		} else if cachedOnly {
			stats.PreservedImages++
			return
		} else {
			if !encodingbudget.TryAcquire() {
				stats.BusyImages++
				return
			}
			stats.ColdEncodes++
			decoded, err := base64.StdEncoding.AppendDecode(nil, input)
			if err == nil {
				if optimized := recompressPNG(workCtx, decoded); optimized != nil {
					data = base64.StdEncoding.EncodeToString(optimized)
				}
			}
			encodingbudget.Release()
			if workCtx.Err() != nil {
				stats.BudgetExhausted = true
				stats.PreservedImages++
				if ctx.Err() == nil {
					o.cacheResult(key, "", time.Now(), compressionRetryDelay)
				}
				return
			}
			o.put(key, data, time.Now())
		}
		if data == "" {
			stats.PreservedImages++
			return
		}
		encoded, _ := json.Marshal(part.prefix + data)
		if len(encoded) >= part.end-part.start {
			return
		}
		changes = append(changes, replacement{part.start, part.end, encoded})
		stats.OptimizedImages++
	}
	if a == nil {
		// Standalone callers need a single pass, not a persistent offset index.
		walk(body, func(part imagePart) {
			pos, ok := part.pngLocation(body)
			if !ok {
				stats.PreservedImages++
				return
			}
			visit(pos)
		})
	} else {
		stats.PreservedImages = a.eligible - len(a.pngs)
		for _, part := range a.pngs {
			visit(part)
		}
	}
	if len(changes) != 0 && ctx.Err() == nil {
		sort.Slice(changes, func(i, j int) bool { return changes[i].start < changes[j].start })
		size := len(body)
		for _, change := range changes {
			size += len(change.data) - (change.end - change.start)
		}
		out, pos := make([]byte, 0, size), 0
		for _, change := range changes {
			out = append(out, body[pos:change.start]...)
			out = append(out, change.data...)
			pos = change.end
		}
		body = append(out, body[pos:]...)
		stats.OutputBytes = len(body)
	}
	stats.DurationMs = time.Since(start).Milliseconds()
	if ctx.Err() != nil {
		stats.OptimizedImages = 0
	}
	return body, stats
}

func (o *Optimizer) get(key cacheKey, now time.Time) (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	entry := o.entries[key]
	if entry == nil {
		return "", false
	}
	value := entry.Value.(cacheEntry)
	if !now.Before(value.expires) {
		o.remove(entry)
		return "", false
	}
	o.lru.MoveToBack(entry)
	return value.data, true
}

func (o *Optimizer) put(key cacheKey, data string, now time.Time) {
	o.cacheResult(key, data, now, cacheTTL)
}

func (o *Optimizer) cacheResult(key cacheKey, data string, now time.Time, ttl time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if existing := o.entries[key]; existing != nil {
		// A concurrent completed compression takes precedence over a timed-out attempt.
		entry, ok := existing.Value.(cacheEntry)
		if ok && ttl == compressionRetryDelay && now.Before(entry.expires) {
			return
		}
		o.remove(existing)
	}
	if len(data) > maxCacheBytes {
		return
	}
	for o.lru.Len() > 0 && (o.bytes+len(data) > maxCacheBytes || o.lru.Len() >= maxCacheEntries || !now.Before(o.lru.Front().Value.(cacheEntry).expires)) {
		o.remove(o.lru.Front())
	}
	o.entries[key] = o.lru.PushBack(cacheEntry{key, data, now.Add(ttl)})
	o.bytes += len(data)
}

func (o *Optimizer) remove(entry *list.Element) {
	value := entry.Value.(cacheEntry)
	o.bytes -= len(value.data)
	delete(o.entries, value.key)
	o.lru.Remove(entry)
}
