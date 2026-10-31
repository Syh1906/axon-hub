package responses

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

type transportSessionKey struct{}
type transportSession struct{ http sync.Map }

// WithTransportSession scopes WS downgrade decisions to one downstream connection.
func WithTransportSession(ctx context.Context) context.Context {
	return context.WithValue(ctx, transportSessionKey{}, &transportSession{})
}

type transportExecutor struct {
	http          pipeline.Executor
	ws            *WebSocketExecutor
	stripPrevious bool
}

// NewTransportExecutor follows the downstream transport. ws==nil declares HTTP only.
func NewTransportExecutor(http pipeline.Executor, ws *WebSocketExecutor, stripPrevious bool) pipeline.Executor {
	return &transportExecutor{http: http, ws: ws, stripPrevious: stripPrevious}
}

func (e *transportExecutor) Do(ctx context.Context, r *httpclient.Request) (*httpclient.Response, error) {
	if !e.useWebSocket(ctx, r) {
		reportTransport(r, TransportHTTP, e.reason(ctx, r))
		return e.doHTTP(ctx, r)
	}
	reportTransport(r, TransportWebSocket, "")
	result, err := e.ws.Do(ctx, r)
	if !canFallbackWebSocket(ctx, err) {
		return result, err
	}
	e.downgrade(ctx, r)
	reportTransport(r, TransportHTTP, "websocket_handshake_failed")
	return e.doHTTP(ctx, r)
}

func (e *transportExecutor) DoStream(ctx context.Context, r *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
	if !e.useWebSocket(ctx, r) {
		reportTransport(r, TransportHTTP, e.reason(ctx, r))
		return e.streamHTTP(ctx, r)
	}
	reportTransport(r, TransportWebSocket, "")
	result, err := e.ws.DoStream(ctx, r)
	if !canFallbackWebSocket(ctx, err) {
		return result, err
	}
	e.downgrade(ctx, r)
	reportTransport(r, TransportHTTP, "websocket_handshake_failed")
	return e.streamHTTP(ctx, r)
}

func (e *transportExecutor) doHTTP(ctx context.Context, r *httpclient.Request) (*httpclient.Response, error) {
	if err := e.checkContinuation(ctx, r); err != nil {
		return nil, err
	}
	return e.http.Do(ctx, e.httpRequest(r))
}
func (e *transportExecutor) streamHTTP(ctx context.Context, r *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
	if err := e.checkContinuation(ctx, r); err != nil {
		return nil, err
	}
	return e.http.DoStream(ctx, e.httpRequest(r))
}
func (e *transportExecutor) checkContinuation(ctx context.Context, r *httpclient.Request) error {
	// The orchestrator removes previous_response_id only after restoring the history.
	if (e.stripPrevious || shared.IsResponsesWebSocket(ctx)) && gjson.GetBytes(r.Body, "previous_response_id").String() != "" {
		if err := shared.ResponsesRestoreError(ctx); err != nil {
			return fmt.Errorf("%w: %w", httpclient.ErrContinuationRestoreFailed, err)
		}
		return httpclient.ErrContinuationUnavailable
	}
	return nil
}

func (e *transportExecutor) useWebSocket(ctx context.Context, r *httpclient.Request) bool {
	return e.ws != nil && shared.IsResponsesWebSocket(ctx) && !e.downgraded(ctx, r)
}

func (e *transportExecutor) downgraded(ctx context.Context, r *httpclient.Request) bool {
	if session, ok := ctx.Value(transportSessionKey{}).(*transportSession); ok {
		_, found := session.http.Load(r.URL)
		return found
	}
	return false
}

func (e *transportExecutor) downgrade(ctx context.Context, r *httpclient.Request) {
	if session, ok := ctx.Value(transportSessionKey{}).(*transportSession); ok {
		session.http.Store(r.URL, true)
	}
}

func (e *transportExecutor) reason(ctx context.Context, r *httpclient.Request) string {
	if !shared.IsResponsesWebSocket(ctx) {
		return ""
	}
	if e.ws == nil {
		return "websocket_not_enabled"
	}
	if e.downgraded(ctx, r) {
		return "connection_uses_http_fallback"
	}
	return ""
}

func (e *transportExecutor) httpRequest(r *httpclient.Request) *httpclient.Request {
	result := PrepareHTTPTransportRequest(r, e.stripPrevious)
	u, err := url.Parse(result.URL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") {
		return result
	}
	copy := *result
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	}
	copy.URL = u.String()
	return &copy
}

func reportTransport(r *httpclient.Request, protocol, reason string) {
	if r.OnTransportSelected != nil {
		r.OnTransportSelected(protocol, reason)
	}
}

// Only a failed handshake is replayable. A write error is NOT proof of non-delivery.
type webSocketHandshakeError struct{ cause error }

func (e *webSocketHandshakeError) Error() string {
	return fmt.Sprintf("websocket handshake failed: %v", e.cause)
}
func (e *webSocketHandshakeError) Unwrap() error { return e.cause }

func canFallbackWebSocket(ctx context.Context, err error) bool {
	var handshake *webSocketHandshakeError
	if err == nil || ctx.Err() != nil || !errors.As(err, &handshake) {
		return false
	}
	var upstream *httpclient.Error
	if !errors.As(err, &upstream) {
		return true
	}
	// Authentication, rate limits and ordinary parameter errors retain their semantics.
	switch upstream.StatusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusUpgradeRequired,
		http.StatusNotImplemented, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return upstream.StatusCode == http.StatusOK && strings.Contains(strings.ToLower(upstream.Headers.Get("Content-Type")), "json")
}
