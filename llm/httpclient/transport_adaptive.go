package httpclient

import (
	"context"
	"time"
)

// These are conservative transport heuristics, not provider response deadlines.
// Learning is process-local and never depends on a channel name or model.
const (
	maxUploadPools        = 8
	minAdaptiveBodyBytes  = 64 << 10
	slowUploadBytesPerSec = 256 << 10
	minSlowUploadTime     = 2 * time.Second
	uploadProbeLifetime   = time.Minute
	uploadProbeCooldown   = time.Minute
	uploadExpansionGap    = 10 * time.Second
)

type originPools struct {
	lanes        []*originPool
	next         uint64
	probe        *uploadPoolProbe
	blockedUntil time.Time
}

type uploadPoolProbe struct {
	lane       *originPool
	generation *poolGeneration
	bytes      int64
	elapsed    time.Duration
	deadline   time.Time
	confirmed  int
}

type uploadStall struct {
	bytes    int64
	elapsed  time.Duration
	observed time.Time
}

// poolUpload is protected by recoveringTransport.mu. An attempt stays owned by
// its generation until its response ends, but stops being an upload at headers
// or WroteRequest. This excludes reasoning/response waiting from expansion.
type poolUpload struct {
	ctx          context.Context
	bytes        int64
	connectionID uint64
	protocol     string
	connected    time.Time
	uploaded     time.Time
	headers      bool
	status       int
	failed       bool
	released     bool
	probeCounted bool
}

func (u *poolUpload) resetConnection() {
	u.connectionID, u.protocol = 0, ""
	u.connected, u.uploaded = time.Time{}, time.Time{}
	u.headers, u.failed, u.probeCounted = false, false, false
	u.status = 0
}

func (u *poolUpload) pending() bool {
	return !u.released && !u.failed && !u.headers && u.uploaded.IsZero() && u.bytes > 0 && u.ctx.Err() == nil
}

func (u *poolUpload) pendingHTTP2() bool {
	return u.pending() && u.protocol == "HTTP/2.0" && u.connectionID != 0 && !u.connected.IsZero()
}

func (g *poolGeneration) hasHTTP2Upload() bool {
	for upload := range g.attempts {
		if upload.pendingHTTP2() {
			return true
		}
	}
	return false
}

func (g *poolGeneration) pendingUploads() int {
	count := 0
	for upload := range g.attempts {
		if upload.pending() {
			count++
		}
	}
	return count
}

// stalledSharedUpload requires two actual uploads on the same HTTP/2 socket.
// Full body size / elapsed time is an upper bound on average upload speed while
// WroteRequest is still absent. DNS, TLS, unknown bodies and completed uploads
// do not provide this evidence. Prefer the smallest comparable body for probes.
func (g *poolGeneration) stalledSharedUpload(now time.Time) *poolUpload {
	var candidate *poolUpload
	for upload := range g.attempts {
		if !upload.pendingHTTP2() || upload.bytes < minAdaptiveBodyBytes {
			continue
		}
		elapsed := now.Sub(upload.connected)
		if elapsed < minSlowUploadTime || float64(upload.bytes)/elapsed.Seconds() > slowUploadBytesPerSec {
			continue
		}
		shared := false
		for other := range g.attempts {
			if other != upload && other.pending() && other.connectionID == upload.connectionID {
				shared = true
				break
			}
		}
		if shared && (candidate == nil || upload.bytes < candidate.bytes) {
			candidate = upload
		}
	}
	return candidate
}

// chooseUploadPool runs only when real work arrives. It creates no synthetic
// probes, queues or background scaler. Existing idle capacity is used first.
// The caller holds t.mu and reserves the chosen generation before unlocking.
func (t *recoveringTransport) chooseUploadPool(pools *originPools, now time.Time) (*originPool, string) {
	action := ""
	if probe := pools.probe; probe != nil && (!now.Before(probe.deadline) || probe.lane.current != probe.generation) {
		pools.rejectProbe(now)
		action = "expansion_unverified"
	}
	index := int(pools.next % uint64(len(pools.lanes)))
	pools.next++
	var best *originPool
	pending := 0
	for offset := 0; offset < len(pools.lanes); offset++ {
		lane := pools.lanes[(index+offset)%len(pools.lanes)]
		if lane.retiring {
			continue
		}
		count := lane.current.pendingUploads()
		if best == nil || count < pending || count == pending && lane.current.active < best.current.active {
			best, pending = lane, count
		}
	}
	// The last admitting lane may have idled out while a rejected probe still
	// drains a long response. Reopen baseline capacity without reviving that probe.
	if best == nil {
		best = &originPool{current: t.newGeneration()}
		pools.lanes = append(pools.lanes, best)
		return best, "baseline_reopened"
	}
	// Respect explicitly supplied connection budgets. Independent pools would
	// otherwise multiply a caller's MaxConnsPerHost limit behind its back.
	if t.template.DisableKeepAlives || t.template.MaxConnsPerHost > 0 ||
		pending == 0 || len(pools.lanes) >= maxUploadPools || pools.probe != nil || now.Before(pools.blockedUntil) || best.draining != nil {
		return best, action
	}
	evidence := best.lastStall
	if evidence != nil && now.Sub(evidence.observed) >= uploadProbeLifetime {
		best.lastStall, evidence = nil, nil
	}
	if evidence != nil && !best.current.hasHTTP2Upload() {
		// Past HTTP/2 pressure does not establish the protocol or upload phase of
		// a fresh burst: the old connection may have closed or negotiated HTTP/1.
		evidence = nil
	}
	if stalled := best.current.stalledSharedUpload(now); stalled != nil {
		evidence = &uploadStall{bytes: stalled.bytes, elapsed: now.Sub(stalled.connected), observed: now}
	}
	if evidence == nil {
		return best, action
	}
	lane := &originPool{current: t.newGeneration()}
	pools.lanes = append(pools.lanes, lane)
	pools.probe = &uploadPoolProbe{
		lane: lane, generation: lane.current, bytes: evidence.bytes,
		elapsed: evidence.elapsed, deadline: now.Add(uploadProbeLifetime),
	}
	pools.blockedUntil = now.Add(uploadExpansionGap)
	return lane, "expanded"
}

// observeUploadProbe validates expansion using two naturally arriving uploads
// at least as large as the stalled one, each taking >=25% less time per byte. A small ping
// cannot validate a large upload. Fast headers alone or rejected work cannot
// justify more connections. The caller holds t.mu; no diagnostic locks here.
func (t *recoveringTransport) observeUploadProbe(pools *originPools, lane *originPool, generation *poolGeneration, upload *poolUpload, now time.Time) string {
	probe := pools.probe
	if probe == nil || probe.lane != lane || probe.generation != generation || lane.current != generation ||
		upload.ctx.Err() != nil || upload.probeCounted {
		return ""
	}
	// HTTP/2 may finish the response before WroteRequest runs. Releasing the
	// response removes upload pressure, but does not invalidate a later write
	// result for this still-current probe. Cancellation remains excluded.
	if !now.Before(probe.deadline) {
		pools.rejectProbe(now)
		return "expansion_unverified"
	}
	if upload.failed || upload.headers && upload.status >= 400 {
		pools.rejectProbe(now)
		return "expansion_unverified"
	}
	if !upload.headers || upload.uploaded.IsZero() || upload.connected.IsZero() ||
		upload.bytes < probe.bytes || upload.protocol != "HTTP/2.0" {
		return ""
	}
	upload.probeCounted = true
	elapsed := upload.uploaded.Sub(upload.connected)
	if elapsed < 0 || float64(elapsed)/float64(upload.bytes) > .75*float64(probe.elapsed)/float64(probe.bytes) {
		pools.rejectProbe(now)
		return "expansion_unhelpful"
	}
	probe.confirmed++
	if probe.confirmed < 2 {
		return ""
	}
	pools.probe = nil
	return "expansion_verified"
}

func (p *originPools) rejectProbe(now time.Time) {
	// Keep at least one admitting pool if the original has already idled out.
	for _, lane := range p.lanes {
		if lane != p.probe.lane && !lane.retiring {
			p.probe.lane.retiring = true
			break
		}
	}
	p.probe = nil
	p.blockedUntil = now.Add(uploadProbeCooldown)
}
