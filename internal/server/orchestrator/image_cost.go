package orchestrator

import (
	"context"
	"time"

	"github.com/looplj/axonhub/internal/pkg/imagebilling"
	"github.com/looplj/axonhub/internal/server/biz"
)

func captureImageCostEvidence(ctx context.Context, state *PersistenceState, body []byte) {
	if state == nil || state.RequestExec == nil || !biz.NeedsImageCostEvidence(state.RequestExec.CostSnapshot) {
		return
	}
	var request []byte
	if state.RawProviderRequest != nil {
		request = state.RawProviderRequest.Body
		if len(state.RawProviderRequest.JSONBody) > 0 {
			request = state.RawProviderRequest.JSONBody
		}
	}
	inspectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state.RequestExec.CostSnapshot.Images = imagebilling.Extract(inspectCtx, request, body, state.RequestExec.ModelID, imagebilling.Fetch)
}
