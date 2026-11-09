package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

func TestResponsesAffinityRequiresSuccessfulStreamDelivery(t *testing.T) {
	for _, mode := range []string{"delivered", "canceled", "write_failed"} {
		t.Run(mode, func(t *testing.T) {
			executor := &mockExecutor{streamEvents: []*httpclient.StreamEvent{
				{Data: []byte(`{"type":"response.created","response":{"id":"resp_affinity","object":"response","status":"in_progress","model":"gpt-4","output":[]}}`)},
				{Data: []byte(`{"type":"response.output_text.delta","item_id":"msg_title","output_index":0,"content_index":0,"delta":"answer"}`)},
				{Data: []byte(`{"type":"response.completed","response":{"id":"resp_affinity","object":"response","status":"completed","model":"gpt-4","output":[{"type":"message","id":"msg_title","role":"assistant","status":"completed","content":[{"type":"output_text","text":"answer","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)},
			}}
			ctx, processor, _, source := responsesOriginGateway(t, executor)
			const previousChannel = 999
			input := []byte(`{"model":"gpt-4","stream":true,"input":[{"type":"reasoning","encrypted_content":"shared-history","summary":[]}]}`)
			require.NoError(t, processor.RequestService.RecordResponsesAffinity(ctx,
				&ent.RequestExecution{ChannelID: previousChannel, Selection: &objects.RequestExecutionSelection{}}, input,
				[]byte(`{"id":"old","status":"completed","output":[]}`)))
			requestCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			result, err := processor.Process(requestCtx, &httpclient.Request{Method: "POST", Body: input})
			require.NoError(t, err)
			boundary := result.ChatCompletionStream.(streams.DeliveryBoundary)
			terminalSeen := false
			for result.ChatCompletionStream.Next() {
				event := result.ChatCompletionStream.Current()
				terminal := gjson.GetBytes(event.Data, "type").String() == "response.completed"
				if terminal {
					terminalSeen = true
					route, found, err := processor.RequestService.GetResponsesAffinity(ctx, "gpt-4", "", "shared-history")
					require.NoError(t, err)
					require.True(t, found)
					require.Equal(t, previousChannel, route.ChannelID, "reading completion is not evidence of delivery")
					if mode == "canceled" {
						cancel()
					}
				}
				err = boundary.Deliver(requestCtx, func() (bool, error) {
					if terminal && mode == "write_failed" {
						return false, errors.New("socket write failed")
					}
					return terminal, nil
				})
				if terminal && mode != "delivered" {
					require.Error(t, err)
					break
				}
				require.NoError(t, err)
			}
			require.True(t, terminalSeen)
			_ = result.ChatCompletionStream.Close()
			route, found, err := processor.RequestService.GetResponsesAffinity(ctx, "gpt-4", "", "shared-history")
			require.NoError(t, err)
			require.True(t, found)
			if mode == "delivered" {
				require.Equal(t, source.Channel.ID, route.ChannelID)
			} else {
				require.Equal(t, previousChannel, route.ChannelID)
			}
			_, found, err = processor.RequestService.GetResponsesContinuationRoute(ctx, "resp_affinity")
			require.NoError(t, err)
			require.True(t, found, "durable origin evidence survives failed delivery")
		})
	}
}
