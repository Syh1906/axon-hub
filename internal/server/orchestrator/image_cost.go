package orchestrator

import (
	"context"
	"time"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/imagebilling"
	"github.com/looplj/axonhub/internal/server/biz"
)

func captureImageCostEvidence(ctx context.Context, state *PersistenceState, body []byte) {
	if state == nil || state.RequestExec == nil {
		return
	}
	var request []byte
	if state.RawProviderRequest != nil {
		request = state.RawProviderRequest.Body
		if len(state.RawProviderRequest.JSONBody) > 0 {
			request = state.RawProviderRequest.JSONBody
		}
	}
	// Output evidence also excludes images from text speed statistics. Only
	// image pricing needs a network read of the final image header.
	var fetch imagebilling.FetchImage
	if biz.NeedsImageCostEvidence(state.RequestExec.CostSnapshot) {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		fetch = imagebilling.Fetch
	}
	images := imagebilling.Extract(ctx, request, body, state.RequestExec.ModelID, fetch)
	if state.RequestExec.CostSnapshot == nil {
		if len(images) == 0 {
			return
		}
		state.RequestExec.CostSnapshot = &objects.RequestCostSnapshot{}
	}
	state.RequestExec.CostSnapshot.Images = images
}
