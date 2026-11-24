package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/requestpayload"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
)

const optimizationMetadataKey = "axon_payload_optimization_v1"

func optimizeRequestTransport(outbound *PersistentOutboundTransformer) pipeline.Middleware {
	return pipeline.OnRawRequest("optimize-request-transport", func(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
		state, channel := outbound.state, outbound.GetCurrentChannel()
		if channel == nil || state.Request == nil {
			return request, nil
		}
		request.EncodingScope, request.EncodingGeneration = channel.ID, channel.UpdatedAt.UnixNano()
		request.LearnHTTP1 = true
		transport := state.settings().UpstreamTransport
		request.ProtocolPreference = transport.Protocol
		overrides := channel.GetHeaderOverrideOperations()
		request.ProtocolVaryHeaders = nil
		for _, op := range overrides {
			switch op.Op {
			case objects.OverrideOpCopy, objects.OverrideOpRename:
				request.ProtocolVaryHeaders = append(request.ProtocolVaryHeaders, op.From, op.To)
			default:
				request.ProtocolVaryHeaders = append(request.ProtocolVaryHeaders, op.Path)
			}
		}
		payload := state.RequestService.PayloadService
		request.PeerDictionary = payload.PeerRequest(ctx)
		request.NoZstdEndpoints = payload.DisabledZstdEndpoints()
		switch llm.APIFormat(request.APIFormat) {
		case llm.APIFormatOpenAIChatCompletion, llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIResponseWebSocket, llm.APIFormatOpenAIResponseCompact, llm.APIFormatAnthropicMessage, llm.APIFormatGeminiContents:
		default:
			return request, nil
		}
		outbound.applyAddressDeadline(ctx, request)
		request.LearnZstd = transport.AutomaticCompression && payload.LearnRequestZstd() && len(overrides) == 0
		if request.APIFormat == string(llm.APIFormatOpenAIResponse) || request.APIFormat == string(llm.APIFormatOpenAIResponseWebSocket) {
			request.ZstdEndpoints = payload.ResponsesZstdEndpoints()
			// The HTTP transport applies this only if the executor actually sends an HTTPS POST.
			request.HTTP1Endpoints = payload.ResponsesHTTP1Endpoints()
		}
		body, stats := state.RequestService.OptimizeRequestImages(ctx, state.Request.ProjectID, channel.ID, request.Body, func() httpclient.UploadEstimate {
			if state.Proxy != nil || request.APIFormat == string(llm.APIFormatOpenAIResponseWebSocket) {
				return httpclient.UploadEstimate{}
			}
			request.MeasurePlainUpload = true
			return channel.HTTPClient.EstimateUpload(request)
		})
		if stats.ColdEncodes > 0 && stats.OutputBytes < stats.InputBytes {
			// A cold PNG pass already paid to shrink this request. Use the new
			// plain-body measurement; a later cache hit can use learned zstd.
			request.LearnZstd = false
		}
		if stats.OutputBytes < stats.InputBytes {
			if bytes.Equal(request.JSONBody, request.Body) {
				request.JSONBody = body
			}
			request.Body = body
			request.Headers.Del("Content-Length")
			// Verification may recover a complete snapshot after plugin overrides.
			// Optimize that final body and retain exactly the bytes actually sent.
			if continuation := state.ResponsesContinuation; continuation != nil {
				continuation.captureAttemptBody(body)
			}
		}
		// Use the actual outgoing size after image optimization, and the backup's
		// own observed upload cost. An unmeasured route remains eligible.
		if limit := request.StartDeadline; !limit.IsZero() && outbound.addressRetryFrom == channel.ID && outbound.addressIndex > 0 &&
			state.Proxy == nil && request.APIFormat != string(llm.APIFormatOpenAIResponseWebSocket) {
			estimate := channel.HTTPClient.EstimateUpload(request)
			remaining := time.Until(limit)
			var expected time.Duration
			addresses := outbound.upstreamAddresses()
			if d := state.routeDecision; d != nil && outbound.addressIndex < len(addresses) && len(d.keys) > 0 &&
				d.keys[0].channel == channel.ID && d.keys[0].work.model == outbound.GetCurrentModelID() &&
				d.keys[0].config == channel.UpdatedAt.UnixNano() && d.keys[0].work.size == routeSizeBucket(len(request.Body)) {
				expected = state.routeHistory.expectedAddressStart(d, addresses[outbound.addressIndex].URL)
			}
			if (expected > 0 && expected >= remaining) || addressUploadExceedsDeadline(len(request.Body), estimate, remaining) {
				return nil, pipeline.ErrBackupStartBudget
			}
		}
		if request.Metadata == nil {
			request.Metadata = make(map[string]string)
		}
		metadata, _ := json.Marshal(stats)
		request.Metadata[optimizationMetadataKey] = string(metadata)
		return request, nil
	})
}

// Every channel using the shared HTTP client can compare protocols on its own
// real traffic. The transport decides eligibility from the final HTTPS request.
func (s *PersistenceState) observeProtocolWork(clock *contexts.RequestClock, request *httpclient.Request) {
	exec := s.RequestExec
	if !request.LearnHTTP1 || clock == nil || !exec.Stream {
		return
	}
	work := httpclient.ProtocolWorkload{Model: exec.ModelID, Format: exec.Format, Ingress: s.Request.Format}
	// Pricing keeps the channel's mapped model. Conditional body overrides
	// can send a different model to the same URL and account.
	if body := bytes.TrimSpace(request.Body); len(body) > 0 && body[0] == '{' {
		if model := gjson.GetBytes(body, "model"); model.Type == gjson.String && model.Str != "" {
			work.Model = model.Str
		}
	}
	if exec.ReasoningEffort != nil {
		work.Effort = *exec.ReasoningEffort
	}
	if exec.CostSnapshot != nil && exec.CostSnapshot.ServiceTier != nil {
		work.Tier = exec.CostSnapshot.ServiceTier.Outbound
	}
	request.ProtocolWork = work
	trace, attempt := request.TransportTrace, s.attemptClock
	clock.OnFinish(func(t contexts.RequestTiming, failed bool) {
		var first time.Time
		if !failed && t.Delivered != nil && s.StreamCompleted && s.StreamTerminalError == nil && len(t.Attempts) == 1 {
			if t.Attempts[0].ExecutionID == attempt.ID {
				first = attempt.ProtocolFirstOutput()
			}
		}
		trace.FinishProtocolObservation(first)
	})
}

func addressUploadExceedsDeadline(bytes int, estimate httpclient.UploadEstimate, remaining time.Duration) bool {
	if remaining <= 0 {
		return true
	}
	predicted := float64(bytes) * estimate.NSPerByte
	if estimate.PreferZstd {
		if estimate.ZstdSavingsNS <= 0 {
			return false // No measured compression cost for this route.
		}
		predicted -= estimate.ZstdSavingsNS
	}
	return predicted > 0 && predicted >= float64(remaining)
}

type executionTransport struct {
	mu                                sync.Mutex
	recorded, recordedInfo            bool
	trace                             *httpclient.TransportTrace
	requestID, executionID, channelID int
	bytes                             int
	optimization                      *requestpayload.Optimization
}

func (t *executionTransport) record(ctx context.Context, failed bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	largeRequest := t.bytes >= 5<<20 || (t.optimization != nil && t.optimization.InputBytes >= 5<<20)
	timings := t.trace.Snapshot()
	slowUpload := timings.UploadMs != nil && *timings.UploadMs >= 2000
	poolChanged := timings.PoolAction != "" && timings.PoolAction != "http1_preferred"
	sampleWS := timings.WebSocket != nil && (timings.WebSocket.CheckoutMs >= 2000 || rand.IntN(32) == 0)
	info := failed || sampleWS || largeRequest || slowUpload || timings.PoolRecovery != "" || poolChanged
	// Transport completion can precede decoding or aggregate-response failure.
	// Permit a debug record to become visible at info once, without duplicates.
	if t.recorded && (t.recordedInfo || !info) {
		return
	}
	t.recorded, t.recordedInfo = true, info
	fields := []log.Field{log.Int("axon_request_id", t.requestID), log.Int("execution_id", t.executionID), log.Int("channel_id", t.channelID), log.Int("request_bytes", t.bytes), log.Any("transport", timings)}
	if t.optimization != nil {
		fields = append(fields, log.Any("image_optimization", t.optimization))
	}
	if info {
		log.Info(ctx, "上游请求分段计时", fields...)
	} else {
		log.Debug(ctx, "上游请求分段计时", fields...)
	}
}

func attachExecutionTransport(state *PersistenceState, request *httpclient.Request) {
	request.TransportTrace = new(httpclient.TransportTrace)
	if d := state.compactionAttempt; d != nil {
		d.trace = request.TransportTrace
	}
	record := &executionTransport{trace: request.TransportTrace, requestID: state.Request.ID, executionID: state.RequestExec.ID, channelID: state.RequestExec.ChannelID, bytes: len(request.Body)}
	if raw := request.Metadata[optimizationMetadataKey]; raw != "" {
		var stats requestpayload.Optimization
		if json.Unmarshal([]byte(raw), &stats) == nil {
			record.optimization = &stats
		}
	}
	state.ExecutionTransport = record
}
