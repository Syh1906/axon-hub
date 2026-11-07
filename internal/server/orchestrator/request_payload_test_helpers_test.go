package orchestrator

import (
	"context"
	"testing"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/stretchr/testify/require"
)

func loadTestRequestResponse(t *testing.T, ctx context.Context, svc *biz.RequestService, req *ent.Request) []byte {
	t.Helper()
	body, err := svc.LoadResponseBody(ctx, req)
	require.NoError(t, err)
	return body
}

func loadTestExecutionResponse(t *testing.T, ctx context.Context, svc *biz.RequestService, exec *ent.RequestExecution) []byte {
	t.Helper()
	body, err := svc.LoadRequestExecutionResponseBody(ctx, exec)
	require.NoError(t, err)
	return body
}
