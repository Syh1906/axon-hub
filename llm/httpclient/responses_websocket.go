package httpclient

import (
	"context"
	"time"
)

// ResponsesWebSocketPolicy separates a deliberate model probe from live traffic.
// HTTPURL is the saved HTTP endpoint after address selection.
type ResponsesWebSocketPolicy struct {
	Probe bool
	// VerifiedHTTP permits an HTTP generation to use a verified pooled WS.
	VerifiedHTTP bool
	NativeOnly   bool
	HTTPReason   string
	HTTPURL      string
	PrepareHTTP  func(*Request) *Request
	// RestoreBeforeWrite rebuilds an owned native continuation after its socket
	// is proven unavailable. It is called at most once, before any frame write.
	RestoreBeforeWrite func(context.Context, *Request) error
	HandshakeTimeout   time.Duration
	Observe            func(WebSocketObservation)
}

// WebSocketObservation contains protocol evidence only, never provider payloads.
type WebSocketObservation struct {
	Stage      string
	Reason     string
	Status     int
	Handshake  time.Duration
	RetryAfter time.Duration
}
