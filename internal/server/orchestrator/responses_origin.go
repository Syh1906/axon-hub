package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/pkg/xcontext"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

func responsesOriginRecorder(ctx context.Context, state *PersistenceState, requestBody []byte) func([]byte) error {
	return func(body []byte) error {
		persistCtx, cancel := xcontext.DetachWithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := state.RequestService.RecordResponsesOrigin(persistCtx, state.RequestExec, requestBody, body); err != nil {
			log.Error(persistCtx, "Unable to retain Responses output origin", log.Cause(err))
			failure := &llm.ResponseError{StatusCode: http.StatusInternalServerError, Cause: err, Detail: llm.ErrorDetail{
				Code: "continuation_origin_persistence_failed", Type: "gateway_error",
				Message: "无法保存本次响应的续聊来源，已停止交付未登记的内容。请检查网关存储。",
			}}
			if state.StreamReturned {
				persistRequestFailure(ctx, state, failure)
			}
			return failure
		}
		return nil
	}
}

type responsesOriginStream struct {
	streams.Stream[*httpclient.StreamEvent]
	record  func([]byte) error
	current *httpclient.StreamEvent
	err     error
}

func (s *responsesOriginStream) Next() bool {
	s.current = nil
	if s.err != nil || !s.Stream.Next() {
		return false
	}
	event := s.Stream.Current()
	if event == nil {
		return true
	}
	kind := gjson.GetBytes(event.Data, "type").String()
	if kind == "" {
		kind = event.Type
	}
	switch kind {
	case "response.output_item.done":
		item := gjson.GetBytes(event.Data, "item")
		if item.Get("encrypted_content").String() != "" {
			// Persist before the consumer receives this completed item, including
			// when the following token/tool generation is canceled or fails.
			body, _ := json.Marshal(struct {
				Status string            `json:"status"`
				Output []json.RawMessage `json:"output"`
			}{"incomplete", []json.RawMessage{json.RawMessage(item.Raw)}})
			s.err = s.record(body)
		}
	case "response.completed", "response.done", "response.incomplete":
		response := gjson.GetBytes(event.Data, "response")
		if response.IsObject() {
			s.err = s.record([]byte(response.Raw))
		}
	}
	if s.err != nil {
		return false
	}
	s.current = event
	return true
}

func (s *responsesOriginStream) Current() *httpclient.StreamEvent { return s.current }

func (s *responsesOriginStream) Err() error {
	if s.err != nil {
		return s.err
	}
	return s.Stream.Err()
}
