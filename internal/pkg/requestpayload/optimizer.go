package requestpayload

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

const (
	minImageBytes           = 64 << 10
	maxImageBytes           = 16 << 20
	maxCacheBytes           = 64 << 20
	maxCacheEntries         = 512
	cacheTTL                = 5 * time.Minute
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
	workers chan struct{}
}

type Optimization struct {
	InputBytes      int   `json:"input_bytes"`
	OutputBytes     int   `json:"output_bytes"`
	OptimizedImages int   `json:"optimized_images"`
	CacheHits       int   `json:"cache_hits"`
	BusyImages      int   `json:"busy_images,omitempty"`
	PreservedImages int   `json:"preserved_images,omitempty"`
	BudgetExhausted bool  `json:"budget_exhausted,omitempty"`
	DurationMs      int64 `json:"duration_ms"`
}

func (o *Optimizer) Optimize(ctx context.Context, project int, body []byte) ([]byte, Optimization) {
	start := time.Now()
	stats := Optimization{InputBytes: len(body), OutputBytes: len(body)}
	if len(body) < minImageBytes {
		return body, stats
	}
	o.once.Do(func() { o.entries = make(map[cacheKey]*list.Element); o.workers = make(chan struct{}, 2) })
	workCtx, stop := context.WithTimeout(ctx, maxOptimizationDuration)
	defer stop()
	type replacement struct {
		start, end int
		data       []byte
	}
	var changes []replacement
	walk(body, func(part imagePart) {
		if workCtx.Err() != nil {
			stats.BudgetExhausted = true
			stats.PreservedImages++
			return
		}
		if part.mime != "image/png" || len(part.data) < minImageBytes || len(part.data) > base64.StdEncoding.EncodedLen(maxImageBytes) || ctx.Err() != nil {
			stats.PreservedImages++
			return
		}
		key := cacheKey{project: project, digest: sha256.Sum256([]byte(part.data))}
		data, hit := o.get(key, time.Now())
		if hit {
			stats.CacheHits++
		} else {
			select {
			case o.workers <- struct{}{}:
			default:
				stats.BusyImages++
				return
			}
			decoded, err := base64.StdEncoding.DecodeString(part.data)
			if err == nil {
				if optimized := recompressPNG(workCtx, decoded); optimized != nil {
					data = base64.StdEncoding.EncodeToString(optimized)
				}
			}
			<-o.workers
			if workCtx.Err() != nil {
				stats.BudgetExhausted = true
				stats.PreservedImages++
				return
			}
			o.put(key, data, time.Now())
		}
		if data == "" {
			stats.PreservedImages++
			return
		}
		encoded, _ := json.Marshal(part.prefix + data)
		pos := part.value.Index
		if pos < 0 || pos+len(part.value.Raw) > len(body) || string(body[pos:pos+len(part.value.Raw)]) != part.value.Raw || len(encoded) >= len(part.value.Raw) {
			return
		}
		changes = append(changes, replacement{pos, pos + len(part.value.Raw), encoded})
		stats.OptimizedImages++
	})
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
	o.mu.Lock()
	defer o.mu.Unlock()
	if existing := o.entries[key]; existing != nil {
		o.remove(existing)
	}
	if len(data) > maxCacheBytes {
		return
	}
	for o.lru.Len() > 0 && (o.bytes+len(data) > maxCacheBytes || o.lru.Len() >= maxCacheEntries || !now.Before(o.lru.Front().Value.(cacheEntry).expires)) {
		o.remove(o.lru.Front())
	}
	o.entries[key] = o.lru.PushBack(cacheEntry{key, data, now.Add(cacheTTL)})
	o.bytes += len(data)
}

func (o *Optimizer) remove(entry *list.Element) {
	value := entry.Value.(cacheEntry)
	o.bytes -= len(value.data)
	delete(o.entries, value.key)
	o.lru.Remove(entry)
}
