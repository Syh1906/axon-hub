package llm

import (
	"encoding/json"
	"strings"
)

func isResponsesAPI(format APIFormat) bool {
	return format == APIFormatOpenAIResponse || format == APIFormatOpenAIResponseWebSocket || format == APIFormatOpenAIResponseCompact
}

// ResponsesOnlyFeature identifies native data with no implemented equivalent
// in another protocol. Known function namespaces and web-search history retain
// their existing cross-protocol mapping.
func ResponsesOnlyFeature(req *Request) string {
	if req == nil {
		return ""
	}
	for _, tool := range req.Tools {
		if tool.Type == ToolTypeResponsesCustomTool || tool.ResponseCustomTool != nil {
			return "custom tool"
		}
	}
	for _, message := range req.Messages {
		if message.ResponsesToolOutput != nil && message.ResponsesToolOutput.Type == "custom_tool_call_output" {
			return "custom tool output"
		}
		for _, call := range message.ToolCalls {
			if call.Type == ToolTypeResponsesCustomTool || call.ResponseCustomToolCall != nil {
				return "custom tool history"
			}
		}
	}
	if req.ProviderExtensions == nil || req.ProviderExtensions.OpenAIResponses == nil || req.ProviderExtensions.OpenAIResponses.Request == nil {
		return ""
	}
	ext := req.ProviderExtensions.OpenAIResponses.Request
	// These fields refer to server-managed history, prompt resources, or
	// processing controls; another protocol cannot reproduce them from messages.
	for _, key := range []string{"conversation", "context_management", "prompt", "moderation"} {
		if raw := ext.RawFields[key]; len(raw) > 0 && string(raw) != "null" {
			return key
		}
	}
	for _, tool := range ext.RawTools {
		if tool.Type == "namespace" {
			var namespace struct {
				Tools []struct {
					Type string `json:"type"`
				} `json:"tools"`
			}
			if json.Unmarshal(tool.Raw, &namespace) == nil {
				allFunctions := true
				for _, sub := range namespace.Tools {
					if sub.Type != "function" {
						allFunctions = false
					}
				}
				if allFunctions {
					continue
				}
			}
		}
		return tool.Type
	}
	for _, item := range ext.RawInputItems {
		if item.Type != "web_search_call" && item.Type != "message" {
			return item.Type
		}
	}
	return ""
}

// AnthropicNativeToolFeature identifies native tool definitions or completed
// server-tool history without an implemented cross-protocol representation.
func AnthropicNativeToolFeature(req *Request) string {
	if req == nil {
		return ""
	}
	if req.TransformerMetadata["anthropic_native_tools"] != nil {
		return "anthropic_native_tools"
	}
	for _, msg := range req.Messages {
		for _, call := range msg.ToolCalls {
			kind, _ := call.TransformerMetadata["anthropic_type"].(string)
			if kind != "tool_use" && strings.HasSuffix(kind, "_tool_use") && !(kind == "server_tool_use" && call.Function.Name == "web_search") {
				return kind
			}
		}
		for _, result := range msg.InlineToolResults {
			kind, _ := result.TransformerMetadata["anthropic_type"].(string)
			if kind != "tool_result" && kind != "web_search_tool_result" && strings.HasSuffix(kind, "_tool_result") {
				return kind
			}
		}
	}
	return ""
}

// UnsupportedRequestFeature returns the first feature that would be silently
// lost by the implemented target converter. It is shared by routing and the
// final conversion boundary; ordinary text/function requests are unaffected.
func UnsupportedRequestFeature(req *Request, target APIFormat) string {
	if req == nil || (req.RequestType != "" && req.RequestType != RequestTypeChat && req.RequestType != RequestTypeCompact) {
		return ""
	}
	if !isResponsesAPI(target) {
		if feature := ResponsesOnlyFeature(req); feature != "" {
			return feature
		}
	}
	if target != APIFormatAnthropicMessage {
		for _, key := range []string{"anthropic_thinking_block_binding", "anthropic_container"} {
			if req.TransformerMetadata[key] != nil {
				return key
			}
		}
		if feature := AnthropicNativeToolFeature(req); feature != "" {
			return feature
		}
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type != "text" {
		if target == APIFormatOllamaChat || (target == APIFormatAnthropicMessage && req.ResponseFormat.Type != "json_schema") {
			return "response_format"
		}
	}
	for _, tool := range req.Tools {
		supported := true
		switch tool.Type {
		case ToolTypeWebSearch:
			supported = isResponsesAPI(target) || target == APIFormatAnthropicMessage || target == APIFormatGeminiContents
		case ToolTypeGoogleSearch:
			supported = isResponsesAPI(target) || target == APIFormatGeminiContents
		case ToolTypeGoogleCodeExecution, ToolTypeGoogleUrlContext:
			supported = target == APIFormatGeminiContents
		case ToolTypeImageGeneration:
			supported = isResponsesAPI(target)
		}
		if !supported {
			return tool.Type
		}
	}
	return ""
}

// HasConversionRequirements determines whether selecting no compatible endpoint
// must remove a candidate instead of falling back to its primary protocol.
func HasConversionRequirements(req *Request) bool {
	if req == nil {
		return false
	}
	if ResponsesOnlyFeature(req) != "" {
		return true
	}
	if AnthropicNativeToolFeature(req) != "" {
		return true
	}
	for _, key := range []string{"anthropic_thinking_block_binding", "anthropic_container"} {
		if req.TransformerMetadata[key] != nil {
			return true
		}
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type != "text" {
		return true
	}
	for _, tool := range req.Tools {
		switch tool.Type {
		case ToolTypeWebSearch, ToolTypeGoogleSearch, ToolTypeGoogleCodeExecution, ToolTypeGoogleUrlContext, ToolTypeImageGeneration:
			return true
		}
	}
	return false
}
