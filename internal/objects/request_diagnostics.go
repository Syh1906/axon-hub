package objects

import (
	"sync/atomic"
	"time"
)

type ChannelWebSocketEvidence struct {
	State      string     `json:"state"`
	Stage      string     `json:"stage,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	Source     string     `json:"source,omitempty"`
	Model      string     `json:"model"`
	Endpoint   string     `json:"endpoint,omitempty"`
	Credential string     `json:"credential,omitempty"`
	CheckedAt  *time.Time `json:"checked_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	StatusCode int        `json:"status_code,omitempty"`
}

// RequestExecutionSelection is an immutable, credential-free snapshot of the
// selected attempt. A later key remark/channel rename cannot rewrite history.
type RequestExecutionSelection struct {
	UseActualModelPrice     bool                      `json:"use_actual_model_price,omitempty"`
	ClientTransport         string                    `json:"client_transport,omitempty"`
	UpstreamTransport       string                    `json:"upstream_transport,omitempty"`
	TransportFallbackReason string                    `json:"transport_fallback_reason,omitempty"`
	ChannelName             string                    `json:"channel_name"`
	Provider                string                    `json:"provider"`
	KeyHint                 string                    `json:"key_hint,omitempty"`
	KeyRemark               string                    `json:"key_remark,omitempty"`
	SessionState            string                    `json:"session_state"`
	Strategy                string                    `json:"strategy"`
	Priority                int                       `json:"priority"`
	Candidates              []RequestRoutingCandidate `json:"candidates"`

	// KeyRef identifies the upstream credential without retaining its value.
	// It is internal routing metadata and is not exposed by GraphQL.
	KeyRef               string                    `json:"key_ref,omitempty"`
	UpstreamRef          string                    `json:"upstream_ref,omitempty"`
	WebSocketProfile     string                    `json:"ws_profile,omitempty"`
	WebSocketEndpoint    string                    `json:"ws_endpoint,omitempty"`
	WebSocketIdentity    string                    `json:"ws_identity,omitempty"`
	WebSocketProbe       bool                      `json:"ws_probe,omitempty"`
	WebSocketEvidence    *ChannelWebSocketEvidence `json:"ws_evidence,omitempty"`
	webSocketObservation *atomic.Pointer[ChannelWebSocketEvidence]

	// An empty dispatch value denotes a legacy row without dispatch tracking.
	Dispatch string `json:"dispatch,omitempty"`
}

// ObserveWebSocket records evidence across selection and dispatch updates. Copies of the
// selection share only this execution's last immutable observation.
func (s *RequestExecutionSelection) ObserveWebSocket() func(*ChannelWebSocketEvidence) {
	s.webSocketObservation = new(atomic.Pointer[ChannelWebSocketEvidence])
	return s.webSocketObservation.Store
}

func (s *RequestExecutionSelection) FinalWebSocketSelection() *RequestExecutionSelection {
	if s == nil || s.webSocketObservation == nil {
		return nil
	}
	value := s.webSocketObservation.Load()
	if value == nil {
		return nil
	}
	copy := *s
	copy.WebSocketEvidence = value
	return &copy
}

const (
	RequestExecutionDispatchPending = "pending"
	RequestExecutionDispatchStarted = "started"
)

type RequestRoutingCandidate struct {
	ChannelID    int      `json:"channel_id"`
	ChannelName  string   `json:"channel_name"`
	Provider     string   `json:"provider"`
	Priority     int      `json:"priority"`
	Models       []string `json:"models"`
	SessionReuse bool     `json:"session_reuse"`
}

// RequestExecutionFailure distinguishes a real upstream HTTP response from a
// gateway/transport error. Body is sanitized before it reaches persistence.
type RequestExecutionFailure struct {
	Source        string `json:"source"`
	Code          string `json:"code,omitempty"`
	Type          string `json:"type,omitempty"`
	Body          string `json:"body,omitempty"`
	BodyTruncated bool   `json:"body_truncated,omitempty"`
}

// RequestFailure also covers admission failures that have no upstream attempt.
type RequestFailure struct {
	Stage   string `json:"stage"`
	Code    string `json:"code,omitempty"`
	Type    string `json:"type,omitempty"`
	Message string `json:"message"`
}
