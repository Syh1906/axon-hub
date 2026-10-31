package orchestrator

import (
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func requestServiceTier(state *PersistenceState, request *httpclient.Request, format llm.APIFormat) *objects.ServiceTierEvidence {
	if format != llm.APIFormatOpenAIChatCompletion && format != llm.APIFormatOpenAIResponse && format != llm.APIFormatOpenAIResponseWebSocket {
		return nil
	}
	e := &objects.ServiceTierEvidence{Settings: objects.ServiceTierSettings{Basis: objects.ServiceTierBasisUpstream}}
	if state.RawRequest != nil {
		e.Client = objects.ServiceTierValue(state.RawRequest.Body)
	}
	body := request.Body
	if len(request.JSONBody) > 0 {
		body = request.JSONBody
	}
	e.Outbound = objects.ServiceTierValue(body)
	return e
}

func observeServiceTier(execution *ent.RequestExecution, body []byte, stream bool) {
	if execution == nil || execution.CostSnapshot == nil || execution.CostSnapshot.ServiceTier == nil {
		return
	}
	e := execution.CostSnapshot.ServiceTier
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
	if _, valid := objects.NormalizeServiceTier(objects.ServiceTierValue(body)); valid && gjson.GetBytes(body, "usage.prompt_tokens").Exists() && gjson.GetBytes(body, "choices").IsArray() && len(gjson.GetBytes(body, "choices").Array()) == 0 {
		if !e.Terminal {
			e.Observed, e.Conflict, e.Upstream, e.LastValid = false, false, "", ""
		}
		e.Terminal = true
		e.Observe(body)
		return
	}
	if e.Terminal {
		return
	}
	e.Observe(body)
}
