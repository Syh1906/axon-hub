package objects

import (
	"fmt"
	"io"
)

// ResponsesServiceTierOverride controls the outgoing request, independently of billing.
// The zero value leaves both client values and generic body overrides unchanged.
type ResponsesServiceTierOverride string

const (
	ResponsesServiceTierInherit   ResponsesServiceTierOverride = ""
	ResponsesServiceTierStandard  ResponsesServiceTierOverride = "default"
	ResponsesServiceTierFast      ResponsesServiceTierOverride = "priority"
	ResponsesServiceTierUltrafast ResponsesServiceTierOverride = "ultrafast"
	ResponsesServiceTierSupported ResponsesServiceTierOverride = "supported"
)

type ResponsesTierSupport struct {
	Fast           bool `json:"fast"`
	Ultrafast      bool `json:"ultrafast"`
	AllowDowngrade bool `json:"allowDowngrade"`
}

// ResponsesTierForRequest is shared by routing, the provider request, and the
// Codex catalog. Inherit callers keep the existing body after generic overrides.
func (s *ChannelSettings) ResponsesTierForRequest(requested string) (string, bool) {
	if s == nil || s.ResponsesServiceTierOverride == ResponsesServiceTierInherit {
		return requested, true
	}
	if s.ResponsesServiceTierOverride == ResponsesServiceTierSupported {
		return s.ResponsesTierSupport.Resolve(requested)
	}
	return string(s.ResponsesServiceTierOverride), true
}

// Resolve returns the wire tier or an empty string for an omitted field.
// An unavailable accelerated tier can only move downward when explicitly allowed.
func (s *ResponsesTierSupport) Resolve(requested string) (string, bool) {
	mode, _ := NormalizeServiceTier(requested)
	switch mode {
	case "ultrafast":
		if s != nil && s.Ultrafast {
			return "ultrafast", true
		}
		if s != nil && s.AllowDowngrade && s.Fast {
			return "priority", true
		}
	case "fast":
		if s != nil && s.Fast {
			return "priority", true
		}
	default:
		return requested, true
	}
	return "", s != nil && s.AllowDowngrade
}

func (m ResponsesServiceTierOverride) Validate() error {
	switch m {
	case ResponsesServiceTierInherit, ResponsesServiceTierStandard, ResponsesServiceTierFast, ResponsesServiceTierUltrafast, ResponsesServiceTierSupported:
		return nil
	default:
		return fmt.Errorf("invalid Responses service tier override: %q", m)
	}
}

func (m ResponsesServiceTierOverride) MarshalGQL(w io.Writer) {
	value := "INHERIT"
	switch m {
	case ResponsesServiceTierStandard:
		value = "STANDARD"
	case ResponsesServiceTierFast:
		value = "FAST"
	case ResponsesServiceTierUltrafast:
		value = "ULTRAFAST"
	case ResponsesServiceTierSupported:
		value = "SUPPORTED"
	}
	_, _ = io.WriteString(w, `"`+value+`"`)
}

func (m *ResponsesServiceTierOverride) UnmarshalGQL(value any) error {
	switch value {
	case "INHERIT":
		*m = ResponsesServiceTierInherit
	case "STANDARD":
		*m = ResponsesServiceTierStandard
	case "FAST":
		*m = ResponsesServiceTierFast
	case "ULTRAFAST":
		*m = ResponsesServiceTierUltrafast
	case "SUPPORTED":
		*m = ResponsesServiceTierSupported
	default:
		return fmt.Errorf("invalid ResponsesServiceTierOverride: %v", value)
	}
	return nil
}
