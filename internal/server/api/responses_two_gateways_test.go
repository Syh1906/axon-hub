package api

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

// The outer gateway uses WS to the inner gateway. Only the inner gateway
// can observe its final HTTP supplier and restore the second turn for it.
func TestResponsesTwoGatewaysWebSocketToHTTP(t *testing.T) {
	var posts atomic.Int32
	bodies := make(chan []byte, 3)
	supplier := responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		b, err := readRequestBody(r)
		require.NoError(t, err)
		bodies <- b
		n := posts.Add(1)
		item := `{"type":"message","id":"msg","role":"assistant","content":[{"type":"output_text","text":"OK"}]}`
		if n == 2 {
			item = `{"type":"function_call","id":"fc","name":"lookup","call_id":"tool-1","arguments":"{}"}`
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":%s}\n\n", item)
		fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r-%d\",\"model\":\"synthetic-model\",\"object\":\"response\",\"status\":\"completed\",\"output\":[%s]}}\n\n", n, item)
	}))
	innerDB, inner, innerProject := newResponsesIntegrationFixture(t, supplier.URL)
	innerSite := responsesIntegrationGateway(t, innerDB, inner, innerProject)
	t.Run("outer", func(t *testing.T) {
		db, h, project := newResponsesIntegrationFixture(t, innerSite.URL, func(c *ent.ChannelCreate) {
			c.SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", SupportsWebSockets: new(true)}})
		})
		ctx, id, tester := fixtureChannelTester(t.Context(), db, h, project.ID)
		proof, err := tester.VerifyChannelWebSocket(ctx, id, "synthetic-model", false)
		require.NoError(t, err)
		require.Equal(t, "verified", proof.State)
		require.EqualValues(t, 1, posts.Load())
		<-bodies
		site := responsesIntegrationGateway(t, db, h, project)
		conn := responsesIntegrationDial(t, site)
		first := responsesIntegrationTurn(t, conn, `{"type":"response.create","model":"synthetic-model","input":"first-question"}`)
		require.Equal(t, "r-2", gjson.GetBytes(first, "response.id").String(), string(first))
		<-bodies
		second := responsesIntegrationTurn(t, conn, `{"type":"response.create","model":"synthetic-model","previous_response_id":"r-2","input":[{"type":"function_call_output","call_id":"tool-1","output":"lookup-result"}]}`)
		require.Equal(t, "r-3", gjson.GetBytes(second, "response.id").String(), string(second))
		body := <-bodies
		require.Contains(t, string(body), "first-question")
		require.Contains(t, string(body), "lookup-result")
		require.Contains(t, string(body), `"type":"function_call"`)
		require.False(t, gjson.GetBytes(body, "previous_response_id").Exists())
		require.EqualValues(t, 3, posts.Load())
		outerRows := db.RequestExecution.Query().AllX(ctx)
		require.Len(t, outerRows, 3)
		for _, row := range outerRows {
			require.Equal(t, "websocket", row.Selection.UpstreamTransport)
		}
		innerRows := innerDB.RequestExecution.Query().AllX(ctx)
		require.Len(t, innerRows, 3)
		for _, row := range innerRows {
			require.Equal(t, "websocket", row.Selection.ClientTransport)
			require.Equal(t, "http", row.Selection.UpstreamTransport)
		}
	})
}
