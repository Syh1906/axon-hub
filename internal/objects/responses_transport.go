package objects

import (
	"fmt"
	"io"
	"strconv"
)

type ResponsesTransportMode string

const (
	ResponsesTransportAuto     ResponsesTransportMode = "AUTO"
	ResponsesTransportPreferWS ResponsesTransportMode = "PREFER_WS"
	ResponsesTransportWSOnly   ResponsesTransportMode = "WS_ONLY"
)

func (m ResponsesTransportMode) Validate() error {
	switch m {
	case "", ResponsesTransportAuto, ResponsesTransportPreferWS, ResponsesTransportWSOnly:
		return nil
	default:
		return fmt.Errorf("invalid Responses transport mode: %q", m)
	}
}

func (m ResponsesTransportMode) MarshalGQL(w io.Writer) {
	value := string(m)
	if m == "" {
		value = string(ResponsesTransportAuto)
	}
	_, _ = io.WriteString(w, strconv.Quote(value))
}

func (m *ResponsesTransportMode) UnmarshalGQL(value any) error {
	v, ok := value.(string)
	if !ok || v == "" {
		return fmt.Errorf("invalid Responses transport mode: %v", value)
	}
	mode := ResponsesTransportMode(v)
	if err := mode.Validate(); err != nil {
		return err
	}
	*m = mode
	return nil
}
