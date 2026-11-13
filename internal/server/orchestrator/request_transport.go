package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/pkg/requestpayload"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
)

const optimizationMetadataKey = "axon_payload_optimization_v1"

func optimizeRequestImages(outbound *PersistentOutboundTransformer) pipeline.Middleware {
	return pipeline.OnRawRequest("optimize-request-images", func(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
		switch llm.APIFormat(request.APIFormat) {
		case llm.APIFormatOpenAIChatCompletion, llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIResponseWebSocket, llm.APIFormatOpenAIResponseCompact, llm.APIFormatAnthropicMessage, llm.APIFormatGeminiContents:
		default:
			return request, nil
		}
		state := outbound.state
		if state.Request == nil {
			return request, nil
		}
		body, stats := state.RequestService.OptimizeRequestImages(ctx, state.Request.ProjectID, request.Body)
		if stats.OutputBytes < stats.InputBytes {
			if bytes.Equal(request.JSONBody, request.Body) {
				request.JSONBody = body
			}
			request.Body = body
			request.Headers.Del("Content-Length")
			// Verification may recover a complete snapshot after plugin overrides.
			// Optimize that final body and retain exactly the bytes actually sent.
			if continuation := state.ResponsesContinuation; continuation != nil {
				continuation.effective = body
				if continuation.snapshotBody != nil {
					continuation.snapshotBody = body
				}
				if continuation.retain {
					input := gjson.GetBytes(body, "input")
					if continuation.lane != nil {
						if !continuation.lane.Budget().Replace(continuation.retentionBytes, len(input.Raw)) {
							continuation.releaseRetention()
							continuation.retain, continuation.input = false, nil
						} else {
							continuation.retentionBytes = len(input.Raw)
						}
					}
					if continuation.retain {
						continuation.input = responseSessionInputItems([]byte(input.Raw))
					}
				}
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
	info := failed || largeRequest
	// Transport completion can precede decoding or aggregate-response failure.
	// Permit a debug record to become visible at info once, without duplicates.
	if t.recorded && (t.recordedInfo || !info) {
		return
	}
	t.recorded, t.recordedInfo = true, info
	fields := []log.Field{log.Int("request_id", t.requestID), log.Int("execution_id", t.executionID), log.Int("channel_id", t.channelID), log.Int("request_bytes", t.bytes), log.Any("transport", t.trace.Snapshot())}
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
	record := &executionTransport{trace: request.TransportTrace, requestID: state.Request.ID, executionID: state.RequestExec.ID, channelID: state.RequestExec.ChannelID, bytes: len(request.Body)}
	if raw := request.Metadata[optimizationMetadataKey]; raw != "" {
		var stats requestpayload.Optimization
		if json.Unmarshal([]byte(raw), &stats) == nil {
			record.optimization = &stats
		}
	}
	state.ExecutionTransport = record
}
