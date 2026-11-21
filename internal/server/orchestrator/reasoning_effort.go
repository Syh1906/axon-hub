package orchestrator

import (
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

// Only the provider's completed response confirms an effort. Intermediate deltas
// and client disconnects do not turn the requested value into a reported value.
func observeResponseEffort(exec *ent.RequestExecution, kind string, body []byte, stream bool) {
	if exec == nil || !responses.IsResponsesFormat(llm.APIFormat(exec.Format)) {
		return
	}
	effort := responses.ReportedReasoningEffort(body, kind, stream)
	if effort == "" {
		return
	}
	if exec.ReasoningEvidence == nil {
		exec.ReasoningEvidence = &objects.RequestReasoningEvidence{}
	}
	exec.ReasoningEvidence.Reported = effort
}
