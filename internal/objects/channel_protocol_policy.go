package objects

import (
	"fmt"
	"slices"

	"github.com/looplj/axonhub/llm"
)

// ChannelProtocolPolicy restricts chat ingress independently of upstream endpoints.
// An absent policy preserves the published automatic conversion behavior.
type ChannelProtocolPolicy struct {
	AllowedProtocols []string `json:"allowedProtocols,omitempty"`
	SameProtocolOnly bool     `json:"sameProtocolOnly,omitempty"`
}

func ChatProtocol(format llm.APIFormat) string {
	switch format {
	case llm.APIFormatOpenAIResponseWebSocket:
		return llm.APIFormatOpenAIResponse.String()
	default:
		return format.String()
	}
}

func IsClientChatProtocol(format string) bool {
	switch llm.APIFormat(format) {
	case llm.APIFormatOpenAIChatCompletion, llm.APIFormatOpenAIResponse, llm.APIFormatAnthropicMessage, llm.APIFormatGeminiContents:
		return true
	default:
		return false
	}
}

func (p *ChannelProtocolPolicy) Validate() error {
	if p == nil {
		return nil
	}
	for _, format := range p.AllowedProtocols {
		if !IsClientChatProtocol(format) {
			return fmt.Errorf("unsupported client chat protocol %q", format)
		}
	}
	return nil
}

func (p *ChannelProtocolPolicy) AllowsClient(format llm.APIFormat) bool {
	return p == nil || len(p.AllowedProtocols) == 0 || slices.Contains(p.AllowedProtocols, ChatProtocol(format))
}

func (p *ChannelProtocolPolicy) AllowsConversion(source, target llm.APIFormat) bool {
	return p == nil || !p.SameProtocolOnly || ChatProtocol(source) == ChatProtocol(target)
}
