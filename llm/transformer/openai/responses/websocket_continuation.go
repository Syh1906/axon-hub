package responses

import (
	"context"
	"net/http"
	"time"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

func (pc *pooledWebSocketConn) canContinue(id string, generation uint64, now time.Time, idleTTL, maxLifetime time.Duration) bool {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return pc.generation == generation && pc.lastResponseID == id && pc.liveForContinuation(now, idleTTL, maxLifetime)
}

// Caller holds pc.mu.
func (pc *pooledWebSocketConn) liveForContinuation(now time.Time, idleTTL, maxLifetime time.Duration) bool {
	return !pc.closed && pc.used &&
		(maxLifetime <= 0 || now.Sub(pc.createdAt) < maxLifetime) &&
		(idleTTL <= 0 || now.Sub(pc.lastUsedAt) < idleTTL)
}

// An old ID has no meaning on a newly dialed store=false connection. Fail before
// the handshake; the orchestrator can rebuild known history before dispatch.
func (e *WebSocketExecutor) acquireContinuation(ctx context.Context, request *httpclient.Request, wsURL string, headers http.Header, id string) (*webSocketLease, error) {
	key, bypass := e.poolKey(ctx, request, wsURL, headers)
	if bypass != "" {
		return nil, shared.ResponsesRecoveryError()
	}
	e.mu.Lock()
	pc := e.pool[key]
	closed := e.closed
	e.mu.Unlock()
	if closed || pc == nil {
		return nil, shared.ResponsesRecoveryError()
	}
	select {
	case pc.inFlight <- struct{}{}:
		pc.mu.Lock()
		live := pc.liveForContinuation(time.Now(), e.idleTTL, e.maxLifetime)
		matches := pc.lastResponseID == id
		used := pc.used
		pc.mu.Unlock()
		if !live || !matches {
			// A stale ID does not invalidate a healthy socket owned by a newer turn.
			// An unused socket may also have been published by a concurrent dial.
			e.releasePooled(pc, used && !live, false)
			return nil, shared.ResponsesRecoveryError()
		}
		return &webSocketLease{conn: pc.conn, owner: e, pooled: pc, reused: true, generation: pc.generation}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
