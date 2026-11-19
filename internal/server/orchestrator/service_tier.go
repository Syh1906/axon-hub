package orchestrator

import (
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func requestServiceTier(state *PersistenceState, request *httpclient.Request, format llm.APIFormat) *objects.ServiceTierEvidence {
	converted := state.LlmRequest != nil && state.CurrentCandidate != nil && convertedResponsesTier(state.CurrentCandidate.Channel.Settings, state.LlmRequest.APIFormat, format)
	if !converted && format != llm.APIFormatOpenAIChatCompletion && !responsesTierFormat(format) {
		return nil
	}
	e := &objects.ServiceTierEvidence{Settings: objects.ServiceTierSettings{Basis: objects.ServiceTierBasisUpstream}}
	body := request.Body
	if len(request.JSONBody) > 0 {
		body = request.JSONBody
	}
	e.Outbound = objects.ServiceTierValue(body)
	if state.RawRequest != nil {
		inbound := state.RawRequest.Body
		if len(inbound) == len(body) && (len(body) == 0 || &inbound[0] == &body[0]) {
			e.Client = e.Outbound
		} else {
			e.Client = objects.ServiceTierValue(inbound)
		}
	}
	e.UseOutboundTier = responsesTierFormat(format) || converted
	return e
}

func observeServiceTier(execution *ent.RequestExecution, body []byte, stream bool) {
	if execution == nil || execution.CostSnapshot == nil || execution.CostSnapshot.ServiceTier == nil {
		return
	}
	e := execution.CostSnapshot.ServiceTier
	if execution.Format == string(llm.APIFormatAnthropicMessage) {
		usage := gjson.GetBytes(body, "usage")
		if stream && gjson.GetBytes(body, "type").String() == "message_start" {
			usage = gjson.GetBytes(body, "message.usage")
		}
		e.ObserveValue(usage.Get("service_tier").String())
		return
	}
	if !stream {
		e.Observe(body)
		return
	}
	if execution.Format == string(llm.APIFormatOpenAIResponse) || execution.Format == string(llm.APIFormatOpenAIResponseWebSocket) {
		switch gjson.GetBytes(body, "type").String() {
		case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled":
			e.Observe([]byte(gjson.GetBytes(body, "response").Raw))
		}
		return
	}
	// A Chat Completions final usage chunk is authoritative when it declares a
	// valid tier. Intermediate declarations remain useful if it omits the field.
	value := objects.ServiceTierValue(body)
	if _, valid := objects.NormalizeServiceTier(value); valid && gjson.GetBytes(body, "usage.prompt_tokens").Exists() && gjson.GetBytes(body, "choices").IsArray() && len(gjson.GetBytes(body, "choices").Array()) == 0 {
		if !e.Terminal {
			e.Observed, e.Conflict, e.Upstream, e.LastValid = false, false, "", ""
		}
		e.Terminal = true
		e.ObserveValue(value)
		return
	}
	if e.Terminal {
		return
	}
	e.ObserveValue(value)
}
