package orchestrator

import (
	"context"

	"github.com/looplj/axonhub/internal/dumper"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
)

// The request and each upstream attempt own distinct wire streams. A transformer
// with custom aggregation must still receive its original chunks.
type streamHistory struct {
	responseChunks []*httpclient.StreamEvent
	count          int
	incremental    transformer.StreamAggregation
}

func newStreamHistory(state *PersistenceState, adapter any) streamHistory {
	var h streamHistory
	if state == nil || state.Settings == nil || state.Settings.StoragePolicy.StoreChunks || dumper.Enabled() {
		return h
	}
	h.incremental = transformer.NewStreamAggregation(adapter, state.RawProviderRequest)
	return h
}

// Once persistence has finished, a closed stream may still be referenced by
// delivery tracking. Neither form of the response history is needed then.
func (h *streamHistory) releaseHistory() {
	h.responseChunks = nil
	h.incremental = nil
}

func (h *streamHistory) add(ctx context.Context, event *httpclient.StreamEvent) {
	h.count++
	if h.incremental != nil {
		h.incremental.Add(ctx, event)
		return
	}
	h.responseChunks = append(h.responseChunks, httpclient.SummarizeBinaryChunk(event))
}
