package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/samber/lo"
	"github.com/tidwall/sjson"
)

type TokenCountInboundTransformer struct{ InboundTransformer }

func NewTokenCountInboundTransformer() *TokenCountInboundTransformer {
	return &TokenCountInboundTransformer{}
}

func (t *TokenCountInboundTransformer) TransformRequest(ctx context.Context, raw *httpclient.Request) (*llm.Request, error) {
	if raw == nil || len(raw.Body) == 0 {
		return nil, fmt.Errorf("%w: request body is required", transformer.ErrInvalidRequest)
	}
	if contentType := raw.Headers.Get("Content-Type"); contentType != "" && !strings.Contains(strings.ToLower(contentType), "application/json") {
		return nil, fmt.Errorf("%w: content type must be application/json", transformer.ErrInvalidRequest)
	}
	var wire MessageRequest
	if err := json.Unmarshal(raw.Body, &wire); err != nil {
		return nil, fmt.Errorf("%w: invalid token count request: %w", transformer.ErrInvalidRequest, err)
	}
	if strings.TrimSpace(wire.Model) == "" || len(wire.Messages) == 0 {
		return nil, fmt.Errorf("%w: model and messages are required", transformer.ErrInvalidRequest)
	}
	if wire.Stream != nil && *wire.Stream {
		return nil, fmt.Errorf("%w: token counting does not support streaming", transformer.ErrInvalidRequest)
	}
	req, err := convertToLLMRequest(&wire)
	if err != nil {
		return nil, err
	}
	req.RequestType = llm.RequestTypeTokenCount
	req.APIFormat = llm.APIFormatAnthropicTokenCount
	req.Stream = lo.ToPtr(false)
	req.MaxTokens = nil
	req.RawRequest = raw
	req.TokenCount = &llm.TokenCountRequest{Body: bytes.Clone(raw.Body)}
	return req, nil
}

func (t *TokenCountInboundTransformer) TransformResponse(_ context.Context, response *llm.Response) (*httpclient.Response, error) {
	if response == nil || response.TokenCount == nil {
		return nil, fmt.Errorf("token count response is missing")
	}
	body, err := json.Marshal(response.TokenCount)
	if err != nil {
		return nil, err
	}
	return &httpclient.Response{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: body}, nil
}

func (t *OutboundTransformer) transformTokenCountRequest(ctx context.Context, req *llm.Request) (*httpclient.Request, error) {
	if req.TokenCount == nil || len(req.TokenCount.Body) == 0 || req.Model == "" {
		return nil, fmt.Errorf("%w: native token count body and model are required", transformer.ErrInvalidRequest)
	}
	if t.config.Type == PlatformBedrock {
		return nil, fmt.Errorf("%w: native Anthropic token counting is not supported by the Bedrock Messages endpoint", transformer.ErrUnsupportedConversion)
	}
	body, err := sjson.SetBytes(bytes.Clone(req.TokenCount.Body), "model", req.Model)
	if err != nil {
		return nil, err
	}
	endpoint := t.config.EndpointPath
	if endpoint == "" {
		endpoint = "/messages"
	}
	if !strings.HasSuffix(endpoint, "/messages") {
		return nil, fmt.Errorf("%w: token counting requires a native messages endpoint", transformer.ErrUnsupportedConversion)
	}
	target := t.config.BaseURL + endpoint + "/count_tokens"
	version := "2023-06-01"
	if t.config.Type == PlatformVertex {
		target = "/v1/messages/count_tokens"
		version = "vertex-2023-10-16"
		body, err = sjson.SetBytes(body, "anthropic_version", version)
		if err != nil {
			return nil, err
		}
	}
	headers := http.Header{"Content-Type": []string{"application/json"}, "Accept": []string{"application/json"}, "Anthropic-Version": []string{version}}
	if req.RawRequest != nil {
		if beta := req.RawRequest.Headers.Get("Anthropic-Beta"); beta != "" {
			headers.Set("Anthropic-Beta", beta)
		}
	}
	var auth *httpclient.AuthConfig
	if t.config.APIKeyProvider != nil {
		if key := t.config.APIKeyProvider.Get(ctx); key != "" {
			auth = &httpclient.AuthConfig{Type: httpclient.AuthTypeAPIKey, HeaderKey: "X-API-Key", APIKey: key}
			if t.config.Type == PlatformLongCat || t.config.Type == PlatformOllama || t.config.Type == PlatformCommandCode {
				auth.Type = httpclient.AuthTypeBearer
				auth.HeaderKey = ""
			}
		}
	}
	return &httpclient.Request{Method: http.MethodPost, URL: target, Headers: headers, Body: body, Auth: auth, RequestType: string(llm.RequestTypeTokenCount), APIFormat: string(llm.APIFormatAnthropicTokenCount)}, nil
}

func transformTokenCountResponse(response *httpclient.Response) (*llm.Response, error) {
	var wire struct {
		InputTokens *int64 `json:"input_tokens"`
	}
	if err := json.Unmarshal(response.Body, &wire); err != nil {
		return nil, fmt.Errorf("invalid token count response: %w", err)
	}
	if wire.InputTokens == nil || *wire.InputTokens < 0 {
		return nil, fmt.Errorf("upstream token count response must contain non-negative input_tokens")
	}
	return &llm.Response{RequestType: llm.RequestTypeTokenCount, APIFormat: llm.APIFormatAnthropicTokenCount, TokenCount: &llm.TokenCountResponse{InputTokens: *wire.InputTokens}}, nil
}
