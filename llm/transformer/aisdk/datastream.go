package aisdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	transformer "github.com/looplj/axonhub/llm/transformer"
)

// DataStreamTransformer implements the AI SDK Data Stream Protocol.
type DataStreamTransformer struct{}

// NewDataStreamTransformer creates a new AI SDK data stream transformer.
func NewDataStreamTransformer() *DataStreamTransformer {
	return &DataStreamTransformer{}
}

// TransformRequest transforms AI SDK request to LLM request.
func (t *DataStreamTransformer) TransformRequest(
	ctx context.Context,
	req *httpclient.Request,
) (*llm.Request, error) {
	// Parse JSON body
	var aiSDKReq Request

	err := json.Unmarshal(req.Body, &aiSDKReq)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to parse AI SDK request: %w", transformer.ErrInvalidRequest, err)
	}

	return convertToLLMRequestWithAPIFormat(&aiSDKReq, nil, llm.APIFormatAiSDKDataStream)
}

// TransformResponse transforms LLM response to AI SDK response.
func (t *DataStreamTransformer) TransformResponse(
	ctx context.Context,
	resp *llm.Response,
) (*httpclient.Response, error) {
	// For data stream protocol, we don't use non-streaming responses
	// This should not be called in streaming mode
	return nil, fmt.Errorf("data stream protocol only supports streaming responses")
}

func (t *DataStreamTransformer) AggregateStreamChunks(
	ctx context.Context,
	chunks []*httpclient.StreamEvent,
) ([]byte, llm.ResponseMeta, error) {
	a := &uiAggregation{result: UIMessage{Role: "assistant"}}
	for _, ev := range chunks {
		a.Add(ctx, ev)
	}
	return a.Result()
}

type uiAggregation struct {
	result                     UIMessage
	meta                       llm.ResponseMeta
	currentText, currentReason strings.Builder
	textOpen, reasoningOpen    bool
	parts                      []UIMessagePart
}

func (a *uiAggregation) Add(ctx context.Context, ev *httpclient.StreamEvent) {

	if ev == nil || len(ev.Data) == 0 {
		return
	}

	// Skip [DONE] marker lines
	if string(ev.Data) == "[DONE]" {
		return
	}

	// Events produced by TransformStream (convert_stream.go) are raw JSON of StreamEvent
	var se StreamEvent
	if err := json.Unmarshal(ev.Data, &se); err != nil {
		// If it's not valid JSON (e.g., SSE formatted), skip for now
		// since current tests use JSON events.
		return
	}

	switch se.Type {
	case "start":
		// Capture message ID
		a.result.ID = se.MessageID
		a.meta.ID = se.MessageID

	case "text-start":
		// Close any open text block defensively
		if a.textOpen {
			a.parts = append(a.parts, UIMessagePart{Type: "text", Text: a.currentText.String()})
			a.currentText.Reset()
		}

		a.textOpen = true

	case "text-delta":
		if a.textOpen {
			a.currentText.WriteString(se.Delta)
		}

	case "text-end":
		if a.textOpen {
			a.parts = append(a.parts, UIMessagePart{Type: "text", Text: a.currentText.String()})
			a.currentText.Reset()

			a.textOpen = false
		}

	case "reasoning-start":
		if a.reasoningOpen {
			a.parts = append(a.parts, UIMessagePart{Type: "reasoning", Text: a.currentReason.String()})
			a.currentReason.Reset()
		}

		a.reasoningOpen = true

	case "reasoning-delta":
		if a.reasoningOpen {
			a.currentReason.WriteString(se.Delta)
		}

	case "reasoning-end":
		if a.reasoningOpen {
			a.parts = append(a.parts, UIMessagePart{Type: "reasoning", Text: a.currentReason.String()})
			a.currentReason.Reset()

			a.reasoningOpen = false
		}

	case "finish-step", "finish":
		// Nothing to aggregate; markers for UI flows.
	case "tool-input-start", "tool-input-delta", "tool-input-available":
		// For now we don't include tool inputs in the aggregated UIMessage parts.
		// Can be added later if needed by consumers.
		return
	default:
		// Ignore unknown types in aggregation
	}
}

func (a *uiAggregation) Result() ([]byte, llm.ResponseMeta, error) {

	parts := a.parts
	// Close any dangling blocks
	if a.textOpen {
		parts = append(parts, UIMessagePart{Type: "text", Text: a.currentText.String()})
	}

	if a.reasoningOpen {
		parts = append(parts, UIMessagePart{Type: "reasoning", Text: a.currentReason.String()})
	}

	a.result.Parts = parts

	b, err := json.Marshal(a.result)
	if err != nil {
		return nil, llm.ResponseMeta{}, fmt.Errorf("failed to marshal aggregated UIMessage: %w", err)
	}

	return b, a.meta, nil
}

func (t *DataStreamTransformer) TransformError(ctx context.Context, rawErr error) *httpclient.Error {
	if rawErr == nil {
		return &httpclient.Error{
			StatusCode: http.StatusInternalServerError,
			Status:     http.StatusText(http.StatusInternalServerError),
			Body:       []byte(`{"message":"internal server error","type":"internal_server_error"}`),
		}
	}

	llmErr, isLLMError := errors.AsType[*llm.ResponseError](rawErr)
	if httpErr, ok := errors.AsType[*httpclient.Error](rawErr); ok && !isLLMError {
		return httpErr
	}

	// Handle validation errors
	if errors.Is(rawErr, transformer.ErrInvalidRequest) {
		return &httpclient.Error{
			StatusCode: http.StatusBadRequest,
			Status:     http.StatusText(http.StatusBadRequest),
			Body:       fmt.Appendf(nil, `{"message":"%s","type":"invalid_request"}`, strings.TrimPrefix(rawErr.Error(), transformer.ErrInvalidRequest.Error()+": ")),
		}
	}

	if isLLMError {
		return &httpclient.Error{
			StatusCode: llmErr.StatusCode,
			Status:     http.StatusText(llmErr.StatusCode),
			Body:       fmt.Appendf(nil, `{"message":"%s","type":"%s"}`, llmErr.Detail.Message, llmErr.Detail.Type),
		}
	}

	return &httpclient.Error{
		StatusCode: http.StatusInternalServerError,
		Status:     http.StatusText(http.StatusInternalServerError),
		Body:       fmt.Appendf(nil, `{"message":"%s","type":"internal_server_error"}`, rawErr.Error()),
	}
}

// generateTextID generates a unique ID for text blocks.
func generateTextID() string {
	return "msg_" + strings.ReplaceAll(uuid.New().String(), "-", "")
}

// SetDataStreamHeaders sets the required headers for AI SDK data stream protocol.
func SetDataStreamHeaders(headers http.Header) {
	headers.Set("X-Vercel-Ai-Ui-Message-Stream", "v1")
	headers.Set("Content-Type", "text/event-stream")
	headers.Set("Cache-Control", "no-cache")
	headers.Set("Connection", "keep-alive")
}

func (t *DataStreamTransformer) NewStreamAggregation(_ *httpclient.Request) transformer.StreamAggregation {
	return &uiAggregation{result: UIMessage{Role: "assistant"}}
}
