package transformer

import (
	"context"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// StreamAggregation consumes provider events in order. Result is called after
// the last event, including any usage delivered in the accounting tail.
type StreamAggregation interface {
	Add(context.Context, *httpclient.StreamEvent)
	Result() ([]byte, llm.ResponseMeta, error)
}

func NewStreamAggregation(adapter any, req *httpclient.Request) StreamAggregation {
	if factory, ok := adapter.(interface {
		NewStreamAggregation(*httpclient.Request) StreamAggregation
	}); ok {
		return factory.NewStreamAggregation(req)
	}
	return nil
}

// MappedStreamAggregation applies a provider's wire envelope conversion one event at a time.
type MappedStreamAggregation struct {
	StreamAggregation
	Map func(*httpclient.StreamEvent) *httpclient.StreamEvent
}

func (m *MappedStreamAggregation) Add(ctx context.Context, e *httpclient.StreamEvent) {
	m.StreamAggregation.Add(ctx, m.Map(e))
}
