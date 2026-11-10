package plugin

import (
	"bytes"
	"encoding/json"
	"maps"

	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm/pipeline"
)

// Fork before either title lane starts. Each lane has independent attempt state
// and a pinned generation, while the request hook result is replayed once without
// invoking JavaScript again. A losing lane cannot release the winner's session.
func (h *requestHookMiddleware) forkForHedge() (orchestrator.RequestHooks, error) {
	source := h.session
	if err := source.gen.acquirePinned(); err != nil {
		return orchestrator.RequestHooks{}, err
	}
	session := &requestHookSession{
		manager: source.manager, gen: source.gen,
		requestJSON: bytes.Clone(source.requestJSON), states: map[int]json.RawMessage{},
		requestOverrides: maps.Clone(source.requestOverrides),
	}
	for id, value := range source.states {
		session.states[id] = bytes.Clone(value)
	}
	request := &requestHookMiddleware{session: session, prepared: bytes.Clone(h.prepared), replay: true}
	return orchestrator.RequestHooks{Request: request, Attempt: pipeline.OnRawRequest("plugin-attempt-hooks", request.OnAttemptRawRequest)}, nil
}
