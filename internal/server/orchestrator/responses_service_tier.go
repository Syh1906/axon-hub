package orchestrator

import (
	"context"
	"fmt"

	"github.com/tidwall/sjson"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
)

// Run after generic body overrides, but before transport finalization and persistence.
// Read the current channel on every attempt; never mutate the shared client request.
func applyResponsesServiceTierOverride(outbound *PersistentOutboundTransformer) pipeline.Middleware {
	return pipeline.OnRawRequest("responses-service-tier-override", func(_ context.Context, request *httpclient.Request) (*httpclient.Request, error) {
		format := llm.APIFormat(request.APIFormat)
		if format != llm.APIFormatOpenAIResponse && format != llm.APIFormatOpenAIResponseWebSocket {
			return request, nil
		}
		settings := outbound.GetCurrentChannel().Settings
		if settings == nil || settings.ResponsesServiceTierOverride == objects.ResponsesServiceTierInherit {
			return request, nil
		}
		body, err := sjson.SetBytes(request.Body, "service_tier", string(settings.ResponsesServiceTierOverride))
		if err != nil {
			return nil, fmt.Errorf("override Responses service tier: %w", err)
		}
		cloned := *request
		cloned.Body = body
		return &cloned, nil
	})
}
