package orchestrator

import (
	"context"
	"fmt"

	"github.com/samber/lo"
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
		if !responsesTierFormat(format) && format != llm.APIFormatOpenAIChatCompletion && !standardChatFormat(format) {
			return request, nil
		}
		channel := outbound.GetCurrentChannel()
		if channel == nil {
			return request, nil
		}
		settings := channel.Settings
		req := outbound.state.LlmRequest
		if !responsesTierFormat(format) && (req == nil || !convertedResponsesTier(settings, req.APIFormat, format)) {
			return request, nil
		}
		if settings == nil || settings.ResponsesServiceTierOverride == objects.ResponsesServiceTierInherit {
			return request, nil
		}
		requested := ""
		if req != nil {
			requested = lo.FromPtr(req.ServiceTier)
		}
		tier, allowed := outboundServiceTier(settings, format, requested)
		if !allowed {
			return nil, unsupportedResponsesTier(requested)
		}
		var body []byte
		var err error
		if tier == "" {
			body, err = sjson.DeleteBytes(request.Body, "service_tier")
		} else {
			body, err = sjson.SetBytes(request.Body, "service_tier", tier)
		}
		if err != nil {
			return nil, fmt.Errorf("override Responses service tier: %w", err)
		}
		cloned := *request
		cloned.Body = body
		return &cloned, nil
	})
}
