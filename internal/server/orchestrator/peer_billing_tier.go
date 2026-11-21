package orchestrator

import (
	"bytes"
	"context"
	"net/http"
	"strconv"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
)

type peerBillingTierMiddleware struct {
	pipeline.DummyMiddleware
	state *PersistenceState
}

func (*peerBillingTierMiddleware) Name() string { return "peer-billing-tier" }

func peerTierFormat(format llm.APIFormat) bool {
	return responsesTierFormat(format) || format == llm.APIFormatOpenAIChatCompletion
}

func (m *peerBillingTierMiddleware) OnOutboundRawRequest(_ context.Context, req *httpclient.Request) (*httpclient.Request, error) {
	exec := m.state.RequestExec
	if exec != nil && peerTierFormat(llm.APIFormat(exec.Format)) && exec.CostSnapshot != nil {
		e := exec.CostSnapshot.ServiceTier
		if e != nil && e.Settings.Enabled && e.Settings.Basis == objects.ServiceTierBasisUpstream {
			req.Headers = req.Headers.Clone()
			if req.Headers == nil {
				req.Headers = make(http.Header)
			}
			req.Headers.Set(httpclient.AcceptBillingTierHeader, "1")
		}
	}
	return req, nil
}

// Return the local pricing decision only to a caller requesting it. A raw
// pass-through response may already contain the previous hop's receipt.
func (m *peerBillingTierMiddleware) receipt() (llm.APIFormat, string) {
	if m.state.LlmRequest == nil || !peerTierFormat(m.state.LlmRequest.APIFormat) {
		return "", ""
	}
	format := m.state.LlmRequest.APIFormat
	exec := m.state.RequestExec
	if m.state.RawRequest == nil || m.state.RawRequest.Headers.Get(httpclient.AcceptBillingTierHeader) != "1" ||
		exec == nil || exec.CostSnapshot == nil {
		return format, ""
	}
	return format, exec.CostSnapshot.ServiceTier.Decision().Mode
}

// Responses carries usage on the final response object. Chat can split the
// finish and usage chunks; the last receipt includes the final usage evidence.
func billingTierPath(format llm.APIFormat, data []byte, stream, chatTail bool) string {
	if !peerTierFormat(format) {
		return ""
	}
	if !stream {
		return httpclient.BillingTierField
	}
	if responsesTierFormat(format) {
		switch gjson.GetBytes(data, "type").String() {
		case "response.completed", "response.done", "response.incomplete", "response.failed", "response.cancelled":
			return "response." + httpclient.BillingTierField
		}
		return ""
	}
	if gjson.GetBytes(data, "usage").IsObject() {
		return httpclient.BillingTierField
	}
	choices := gjson.GetBytes(data, "choices")
	if chatTail && choices.IsArray() {
		return httpclient.BillingTierField
	}
	finished := false
	choices.ForEach(func(_, choice gjson.Result) bool {
		finished = choice.Get("finish_reason").String() != ""
		return !finished
	})
	if finished {
		return httpclient.BillingTierField
	}
	return ""
}

func patchBillingTier(data []byte, path, tier string) ([]byte, error) {
	if tier == "" {
		return sjson.DeleteBytes(data, path)
	}
	return sjson.SetBytes(data, path, tier)
}

func (m *peerBillingTierMiddleware) OnInboundRawResponse(_ context.Context, resp *httpclient.Response) (*httpclient.Response, error) {
	format, tier := m.receipt()
	if format == "" || resp == nil || resp.StatusCode >= 400 || len(resp.Body) == 0 ||
		(tier == "" && !bytes.Contains(resp.Body, []byte(httpclient.BillingTierField))) {
		return resp, nil
	}
	body, err := patchBillingTier(resp.Body, httpclient.BillingTierField, tier)
	if err != nil {
		return nil, err
	}
	result := *resp
	result.Body = body
	if resp.Headers.Get("Content-Length") != "" {
		result.Headers = resp.Headers.Clone()
		result.Headers.Set("Content-Length", strconv.Itoa(len(body)))
	}
	return &result, nil
}

func (m *peerBillingTierMiddleware) OnInboundRawStream(_ context.Context, stream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*httpclient.StreamEvent], error) {
	if m.state.LlmRequest == nil || !peerTierFormat(m.state.LlmRequest.APIFormat) {
		return stream, nil
	}
	requested := m.state.RawRequest != nil && m.state.RawRequest.Headers.Get(httpclient.AcceptBillingTierHeader) == "1"
	var chatTail bool
	adapted := streams.MapErr(stream, func(event *httpclient.StreamEvent) (*httpclient.StreamEvent, error) {
		if event == nil || (!requested && !bytes.Contains(event.Data, []byte(httpclient.BillingTierField))) {
			return event, nil
		}
		format := m.state.LlmRequest.APIFormat
		path := billingTierPath(format, event.Data, true, chatTail || !requested)
		if path == "" {
			return event, nil
		}
		chatTail = format == llm.APIFormatOpenAIChatCompletion
		_, tier := m.receipt()
		body, err := patchBillingTier(event.Data, path, tier)
		if err != nil {
			return nil, err
		}
		result := *event
		result.Data, result.Size = body, 0
		return &result, nil
	})
	return httpclient.WithResponseHeaders(preserveDeliveryCapabilities(stream, adapted), httpclient.GetResponseHeaders(stream)), nil
}
