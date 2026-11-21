package objects

// RequestReasoningEvidence records only values observed on this upstream attempt.
// A missing record distinguishes historical effort metadata from a verified wire value.
type RequestReasoningEvidence struct {
	Requested string `json:"requested,omitempty"`
	Reported  string `json:"reported,omitempty"`
}

func (e *RequestReasoningEvidence) Effective(historical *string) *string {
	if e != nil {
		if e.Reported != "" {
			return &e.Reported
		}
		if e.Requested != "" {
			return &e.Requested
		}
	}
	return historical
}
