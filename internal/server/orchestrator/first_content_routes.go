package orchestrator

import (
	"math/bits"
	"slices"
	"sync"
	"time"
)

const (
	routeHistoryTTL  = 30 * time.Minute
	routeMaxGroups   = 512
	routeMaxSamples  = 64
	routeMinSessions = 20
)

type firstContentStateKey struct{}

type firstContentWork struct {
	project, apiKey, priority, size, load int
	model, effort, tier, format, ingress  string
	websocket                             bool
}

type firstContentRoute struct {
	work    firstContentWork
	channel int
	source  string
	config  int64
}

type firstContentSample struct {
	at           time.Time
	session      uint64
	delay        time.Duration
	failed       bool
	mismatched   bool
	unverified   bool
	tokens       int64
	cachePercent int
	reportedTier string
}

type firstContentRoutes struct {
	mu     sync.Mutex
	groups map[firstContentRoute]firstContentHistory
}

type firstContentHistory struct {
	samples   []firstContentSample
	cached    routeDistribution
	ready     bool
	evaluated time.Time
}

func routeSizeBucket(n int) int {
	// Preserve a common small-request class; compare large bodies by quarter-octaves.
	if n < 64<<10 {
		return 0
	}
	power := bits.Len(uint(n)) - 1
	return 1 + 4*(power-16) + (n-(1<<power))/(1<<(power-2))
}

func (h *firstContentRoutes) record(key firstContentRoute, sample firstContentSample) {
	if !h.mu.TryLock() {
		return
	}
	defer h.mu.Unlock()
	if h.groups == nil {
		h.groups = make(map[firstContentRoute]firstContentHistory)
	}
	group := h.groups[key]
	rows := group.samples
	if rows == nil && len(h.groups) >= routeMaxGroups {
		// Evict a cold group without growing an unbounded per-account index.
		var oldest firstContentRoute
		var at time.Time
		for k, r := range h.groups {
			if last := r.samples[len(r.samples)-1].at; at.IsZero() || last.Before(at) {
				oldest, at = k, last
			}
		}
		delete(h.groups, oldest)
	}
	// Count at most one outcome per session in the window. Retain an observed
	// failure over a success so reconnects cannot conceal a failed round.
	for i, r := range rows {
		if r.session == sample.session {
			if sample.at.Sub(r.at) < routeHistoryTTL && (r.failed || !sample.failed) {
				return
			}
			rows = slices.Delete(rows, i, i+1)
			break
		}
	}
	if len(rows) == routeMaxSamples {
		// Reserve representation for older five-minute windows even when
		// one busy minute alone supplies more than the entire sample budget.
		evict := 0
		if sample.at.Sub(rows[0].at) < routeHistoryTTL {
			var counts, first [7]int
			maxCount := 0
			for i, r := range rows {
				window := r.at.Unix() / 300 % int64(len(counts))
				if counts[window] == 0 {
					first[window] = i
				}
				counts[window]++
				if counts[window] > maxCount {
					maxCount, evict = counts[window], first[window]
				}
			}
		}
		rows = slices.Delete(rows, evict, evict+1)
	}
	group.samples = append(rows, sample)
	// Negative outcomes invalidate a recommendation immediately. Positive samples
	// are folded into the short-lived snapshot at most once per refresh interval.
	if sample.failed || sample.mismatched || sample.unverified {
		group.evaluated = time.Time{}
	}
	h.groups[key] = group
}

type routeDistribution struct {
	n, failures       int
	p50, p95          time.Duration
	tokens            int64
	cache             int
	tier              string
	oldP50, recentP50 time.Duration
}

func distribution(rows []firstContentSample, now time.Time) (routeDistribution, bool) {
	var times [routeMaxSamples]time.Duration
	var old, recent [routeMaxSamples]time.Duration
	oldN, recentN := 0, 0
	var tokens [routeMaxSamples]int64
	var cache [routeMaxSamples]int
	var d routeDistribution
	mismatch := false
	var earliest, latest time.Time
	n := 0
	for _, s := range rows {
		if now.Sub(s.at) >= routeHistoryTTL {
			continue
		}
		d.n++
		if s.failed {
			d.failures++
		}
		if s.mismatched || s.unverified {
			mismatch = true
			continue
		}
		if s.failed {
			continue
		}
		if earliest.IsZero() || s.at.Before(earliest) {
			earliest = s.at
		}
		if s.at.After(latest) {
			latest = s.at
		}
		if n == 0 {
			d.tier = s.reportedTier
		}
		if d.tier != s.reportedTier {
			mismatch = true
		}
		times[n], tokens[n], cache[n] = s.delay, s.tokens, s.cachePercent
		if now.Sub(s.at) >= 5*time.Minute {
			old[oldN] = s.delay
			oldN++
		} else {
			recent[recentN] = s.delay
			recentN++
		}
		n++
	}
	if mismatch || n < routeMinSessions || oldN < 3 || recentN < 3 || latest.Sub(earliest) < 5*time.Minute {
		return d, false
	}
	slices.Sort(times[:n])
	slices.Sort(tokens[:n])
	slices.Sort(cache[:n])
	slices.Sort(old[:oldN])
	slices.Sort(recent[:recentN])
	d.oldP50, d.recentP50 = old[(oldN-1)/2], recent[(recentN-1)/2]
	d.p50, d.p95 = times[(n-1)/2], times[(95*n+99)/100-1]
	d.tokens, d.cache = tokens[(n-1)/2], cache[(n-1)/2]
	return d, true
}

type firstContentDecision struct {
	keys              []firstContentRoute
	chosen, suggested int
	mode              string
	session           uint64
}

// estimateLocked amortizes quantile sorting over new sessions in a busy group.
func (h *firstContentRoutes) estimateLocked(key firstContentRoute, now time.Time) (routeDistribution, bool) {
	g, ok := h.groups[key]
	if !ok {
		return routeDistribution{}, false
	}
	if g.evaluated.IsZero() || now.Sub(g.evaluated) >= 5*time.Second {
		g.cached, g.ready = distribution(g.samples, now)
		g.evaluated = now
		h.groups[key] = g
	}
	return g.cached, g.ready
}

func (h *firstContentRoutes) choose(keys []firstContentRoute, session uint64, percent int, gain time.Duration, now time.Time) *firstContentDecision {
	d := &firstContentDecision{keys: keys, session: session, mode: "observe"}
	if len(keys) < 2 || !h.mu.TryLock() {
		return d
	}
	defer h.mu.Unlock()
	inTrial := percent > 0 && percent < 100 && session%100 < uint64(percent)
	base, ready := h.estimateLocked(keys[0], now)
	best := base
	insufficient := !ready
	for i := 1; i < len(keys); i++ {
		peer, ok := h.estimateLocked(keys[i], now)
		insufficient = insufficient || !ok
		if !ready || !ok || peer.tier == "" || peer.tier != base.tier || peer.tokens*5 < base.tokens*4 || peer.tokens*5 > base.tokens*6 ||
			peer.cache < base.cache-10 || peer.cache > base.cache+10 || peer.failures*base.n > base.failures*peer.n {
			continue
		}
		if peer.p95+gain <= base.p95 && peer.p95 < best.p95 && peer.p50 <= base.p50 &&
			peer.oldP50+gain <= base.oldP50 && peer.recentP50+gain <= base.recentP50 {
			best, d.suggested = peer, i
		}
	}
	if inTrial {
		switch {
		case d.suggested > 0:
			d.mode, d.chosen = "trial", d.suggested
		case insufficient:
			// Opt-in exploration uses one actual new session, never a duplicate POST.
			idx := int((session / 100) % uint64(len(keys)))
			peer, ok := h.estimateLocked(keys[idx], now)
			if (!ready || !ok) && (base.tier == "" || peer.tier == "" || peer.tier == base.tier) &&
				!h.hasUncomparable(keys[0], now) && !h.hasUncomparable(keys[idx], now) && (peer.n == 0 || peer.failures*10 <= peer.n) {
				d.mode, d.chosen = "explore", idx
			}
		}
	}
	return d
}

func (h *firstContentRoutes) hasUncomparable(key firstContentRoute, now time.Time) bool {
	for _, s := range h.groups[key].samples {
		if (s.mismatched || s.unverified) && now.Sub(s.at) < routeHistoryTTL {
			return true
		}
	}
	return false
}

type firstContentCompletion struct {
	model, tier string
	tokens      int64
	cache       int
	valid       bool
}
