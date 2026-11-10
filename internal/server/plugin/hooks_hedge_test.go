package plugin

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/llm"
)

func TestRequestHookHedgeForkReplaysOnceAndOwnsState(t *testing.T) {
	manager := newTestManager(t, newPluginDB(t))
	installed := installHook(t, manager, "fixture.hedge", true, false, `export function onRequest(ctx,req){ctx.state.calls=(ctx.state.calls||0)+1;return {...req,model:req.model+"-once"};}`)
	ctx := contexts.EnsureContainer(pluginCtx())
	hooks := testRequestHooks(t, manager, ctx)
	got, err := hooks.Request.OnInboundLlmRequest(ctx, &llm.Request{Model: "model", RequestType: llm.RequestTypeChat, APIFormat: llm.APIFormatOpenAIChatCompletion})
	require.NoError(t, err)
	require.Equal(t, "model-once", got.Model)
	fork, err := hooks.ForkForHedge()
	require.NoError(t, err)
	defer fork.Request.OnProcessEnd()
	primary := hooks.Request.(*requestHookMiddleware).session
	child := fork.Request.(*requestHookMiddleware).session
	require.JSONEq(t, `{"calls":1}`, string(child.states[installed.ID]))
	primary.commitState(installed.ID, []byte(`{"calls":99}`))
	hooks.Request.OnProcessEnd()
	require.True(t, primary.released)
	require.False(t, child.released)
	copied, err := fork.Request.OnInboundLlmRequest(contexts.ForkRequest(ctx), &llm.Request{Model: "model", RequestType: llm.RequestTypeChat, APIFormat: llm.APIFormatOpenAIChatCompletion})
	require.NoError(t, err)
	require.Equal(t, "model-once", copied.Model)
	require.JSONEq(t, `{"calls":1}`, string(child.states[installed.ID]))
}
