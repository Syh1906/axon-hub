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
)

func (m ResponsesServiceTierOverride) Validate() error {
	switch m {
	case ResponsesServiceTierInherit, ResponsesServiceTierStandard, ResponsesServiceTierFast, ResponsesServiceTierUltrafast:
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
	default:
		return fmt.Errorf("invalid ResponsesServiceTierOverride: %v", value)
	}
	return nil
}
