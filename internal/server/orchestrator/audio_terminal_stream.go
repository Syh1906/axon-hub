package orchestrator

import (
	"bytes"
	"context"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

// Run before raw event capture, so a rejected terminal can neither be
// replayed to the client nor recorded as a successful provider execution.
func validateAudioStreamTerminals(outbound *PersistentOutboundTransformer) pipeline.Middleware {
	return pipeline.OnRawStream("validate-audio-terminal", func(_ context.Context, source streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*httpclient.StreamEvent], error) {
		req := outbound.state.RawProviderRequest
		if req == nil {
			return source, nil
		}
		format := llm.APIFormat(req.APIFormat)
		switch format {
		case llm.APIFormatOpenAISpeech:
			if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(req.Body, "stream_format").String()), "audio") {
				return source, nil // Binary speech has transport EOF rather than SSE events.
			}
		case llm.APIFormatOpenAITranscription, llm.APIFormatOpenAITranslation:
		default:
			return source, nil
		}
		checked := streams.MapErr(source, func(event *httpclient.StreamEvent) (*httpclient.StreamEvent, error) {
			if event == nil {
				return event, nil
			}
			if isNonChoiceTerminalEvent(event) || bytes.HasPrefix(event.Data, llm.DoneStreamEvent.Data) {
				if err := openai.ValidateAudioTerminal(format, event); err != nil {
					return nil, err
				}
			}
			return event, nil
		})
		return httpclient.WithResponseHeaders(checked, httpclient.GetResponseHeaders(source)), nil
	})
}
