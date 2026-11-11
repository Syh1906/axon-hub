package anthropic

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/internal/pkg/xmap"
	"github.com/looplj/axonhub/llm/transformer"
)

const (
	thinkingBlockBindingKey = "anthropic_thinking_block_binding"
	nativeContainerKey      = "anthropic_container"
	nativeStopReasonKey     = "anthropic_stop_reason"
	nativeStopSequenceKey   = "anthropic_stop_sequence"
)

func captureNativeRequestFields(req *MessageRequest, unified *llm.Request) {
	if req.Thinking != nil && req.Thinking.BlockBinding != nil {
		unified.TransformerMetadata[thinkingBlockBindingKey] = req.Thinking.BlockBinding
	}
	if len(req.Container) > 0 && !bytes.Equal(bytes.TrimSpace(req.Container), []byte("null")) {
		unified.TransformerMetadata[nativeContainerKey] = req.Container
	}
	if req.OutputConfig != nil && req.OutputConfig.Format != nil {
		format := req.OutputConfig.Format
		schema, _ := json.Marshal(struct {
			Name   string          `json:"name"`
			Schema json.RawMessage `json:"schema"`
			Strict bool            `json:"strict"`
		}{Name: "response", Schema: format.Schema, Strict: true})
		unified.ResponseFormat = &llm.ResponseFormat{Type: format.Type, JSONSchema: schema}
	}
}

func restoreNativeRequestFields(unified *llm.Request, req *MessageRequest) error {
	if data := nativeJSON(unified.TransformerMetadata, thinkingBlockBindingKey); len(data) > 0 {
		if req.Thinking == nil {
			return fmt.Errorf("%w: %w: thinking.block_binding requires a thinking configuration", transformer.ErrUnsupportedConversion, transformer.ErrInvalidRequest)
		}
		var binding ThinkingBlockBinding
		if err := json.Unmarshal(data, &binding); err != nil {
			return fmt.Errorf("%w: invalid thinking.block_binding", transformer.ErrInvalidRequest)
		}
		req.Thinking.BlockBinding = &binding
	}
	req.Container = nativeJSON(unified.TransformerMetadata, nativeContainerKey)
	if err := restoreNativeTools(unified, req); err != nil {
		return err
	}
	if unified.ResponseFormat == nil || unified.ResponseFormat.Type == "text" {
		return nil
	}
	if unified.ResponseFormat.Type != "json_schema" {
		return fmt.Errorf("%w: %w: Anthropic requires a JSON schema for structured output; response_format %q has no equivalent", transformer.ErrUnsupportedConversion, transformer.ErrInvalidRequest, unified.ResponseFormat.Type)
	}
	var schema struct {
		Schema json.RawMessage `json:"schema"`
	}
	if err := json.Unmarshal(unified.ResponseFormat.JSONSchema, &schema); err != nil || len(schema.Schema) == 0 {
		return fmt.Errorf("%w: response_format.json_schema.schema is required", transformer.ErrInvalidRequest)
	}
	if req.OutputConfig == nil {
		req.OutputConfig = &OutputConfig{}
	}
	req.OutputConfig.Format = &OutputFormat{Type: "json_schema", Schema: schema.Schema}
	return nil
}

func nativeJSON(metadata map[string]any, key string) json.RawMessage {
	value, ok := metadata[key]
	if !ok {
		return nil
	}
	data, _ := json.Marshal(value)
	return data
}

func captureNativeResponseState(resp *llm.Response, reason, sequence *string, container json.RawMessage) {
	keepReason := reason != nil && (*reason == "pause_turn" || *reason == "stop_sequence" || *reason == "model_context_window_exceeded")
	if !keepReason && sequence == nil && len(container) == 0 {
		return
	}
	if resp.TransformerMetadata == nil {
		resp.TransformerMetadata = make(map[string]any)
	}
	if keepReason {
		resp.TransformerMetadata[nativeStopReasonKey] = *reason
	}
	if sequence != nil {
		resp.TransformerMetadata[nativeStopSequenceKey] = *sequence
	}
	if len(container) > 0 {
		resp.TransformerMetadata[nativeContainerKey] = container
	}
}

func restoreNativeResponseState(resp *llm.Response, message *Message) {
	if reason := lo.FromPtr(xmap.GetStringPtr(resp.TransformerMetadata, nativeStopReasonKey)); reason != "" {
		message.StopReason = lo.ToPtr(reason)
	}
	if sequence := lo.FromPtr(xmap.GetStringPtr(resp.TransformerMetadata, nativeStopSequenceKey)); sequence != "" {
		message.StopSequence = lo.ToPtr(sequence)
	}
	message.Container = nativeJSON(resp.TransformerMetadata, nativeContainerKey)
}
