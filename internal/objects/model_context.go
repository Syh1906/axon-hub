package objects

import "fmt"

// ModelContextSettings overrides catalog limits without freezing model metadata.
// Nil fields inherit; they are never persisted as copied upstream values.
type ModelContextSettings struct {
	DefaultContextWindow *int `json:"defaultContextWindow,omitempty"`
	MaxContextWindow     *int `json:"maxContextWindow,omitempty"`
}

type ChannelModelContext struct {
	ModelID              string `json:"modelId"`
	DefaultContextWindow *int   `json:"defaultContextWindow,omitempty"`
	MaxContextWindow     *int   `json:"maxContextWindow,omitempty"`
}

func (s *ModelContextSettings) Validate() error {
	if s == nil {
		return nil
	}
	for _, value := range []*int{s.DefaultContextWindow, s.MaxContextWindow} {
		if value != nil && (*value <= 0 || *value > 2147483647) {
			return fmt.Errorf("context windows must be positive 32-bit integers")
		}
	}
	if s.DefaultContextWindow != nil && s.MaxContextWindow != nil && *s.DefaultContextWindow > *s.MaxContextWindow {
		return fmt.Errorf("default context window must not exceed maximum context window")
	}
	return nil
}
