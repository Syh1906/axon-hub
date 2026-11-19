package httpclient

import "time"

// ResponsesWebSocketPolicy separates a deliberate model probe from live traffic.
// HTTPURL is the saved HTTP endpoint after address selection.
type ResponsesWebSocketPolicy struct {
	Probe            bool
	NativeOnly       bool
	HTTPReason       string
	HTTPURL          string
	PrepareHTTP      func(*Request) *Request
	HandshakeTimeout time.Duration
	Observe          func(WebSocketObservation)
}

// WebSocketObservation contains protocol evidence only, never provider payloads.
type WebSocketObservation struct {
	Stage      string
	Reason     string
	Status     int
	Handshake  time.Duration
	RetryAfter time.Duration
}
