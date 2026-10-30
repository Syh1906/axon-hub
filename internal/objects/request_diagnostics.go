package objects

// RequestExecutionSelection is an immutable, credential-free snapshot of the
// selected attempt. A later key remark/channel rename cannot rewrite history.
type RequestExecutionSelection struct {
	ChannelName  string                    `json:"channel_name"`
	Provider     string                    `json:"provider"`
	KeyHint      string                    `json:"key_hint,omitempty"`
	KeyRemark    string                    `json:"key_remark,omitempty"`
	SessionState string                    `json:"session_state"`
	Strategy     string                    `json:"strategy"`
	Priority     int                       `json:"priority"`
	Candidates   []RequestRoutingCandidate `json:"candidates"`
}

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
