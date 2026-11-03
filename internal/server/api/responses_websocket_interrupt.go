package api

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/looplj/axonhub/internal/server/orchestrator"
)

// 中断在网关结束当前调用；上游只支持 HTTP 时也不需要转发 WS 控制消息。
// 默认等正在输出的项目完成，避免将半个工具调用作为下一轮的历史。
type responsesWebSocketResponse struct {
	mu         sync.Mutex
	dispatcher *responsesWebSocketDispatcher
	streamID   string
	cancel     context.CancelFunc
	id         string
	response   map[string]json.RawMessage
	output     map[int]json.RawMessage
	open       map[int]bool
	sequence   int
	requested  bool
	discard    bool
	terminal   bool
}

func (d *responsesWebSocketDispatcher) handleInterrupt(message []byte) (bool, error) {
	var envelope struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(message, &envelope) != nil || envelope.Type != "response.interrupt" {
		return false, nil
	}
	var interrupt struct {
		ResponseID          string `json:"response_id"`
		DiscardPartialItems bool   `json:"discard_partial_items"`
	}
	if json.Unmarshal(message, &interrupt) != nil || interrupt.ResponseID == "" {
		return true, writeResponsesWebSocketError(d.writer, invalidResponsesWebSocketRequest("response.interrupt requires a response_id and an optional boolean discard_partial_items", "response_id"), "")
	}
	d.responsesMu.Lock()
	response := d.responses[interrupt.ResponseID]
	d.responsesMu.Unlock()
	if response == nil {
		return true, writeResponsesWebSocketError(d.writer, responsesWebSocketRequestError("The response is not available on this WebSocket connection", "response_id", "response_not_found"), "")
	}

	response.mu.Lock()
	defer response.mu.Unlock()
	// 先确认再唤醒流，保证合成的结束事件不会抢在确认之前发送。
	err := writeResponsesWebSocketJSON(d.writer, responsesWebSocketEvent(response.streamID, gin.H{
		"type":            "response.interrupt.accepted",
		"response_id":     interrupt.ResponseID,
		"sequence_number": response.sequence,
	}))
	response.sequence++
	if err != nil || response.terminal {
		// 完成与中断交错时仍确认，不重放已经完成的请求。
		return true, err
	}
	response.requested = true
	response.discard = response.discard || interrupt.DiscardPartialItems
	if response.canInterrupt() {
		response.cancel()
	}
	return true, nil
}

func (r *responsesWebSocketResponse) canInterrupt() bool {
	return r.requested && !r.terminal && (r.discard || len(r.open) == 0)
}

func (r *responsesWebSocketResponse) write(writer *responsesWebSocketWriter, data []byte) error {
	var event map[string]json.RawMessage
	if err := json.Unmarshal(data, &event); err != nil {
		return err
	}
	if event == nil {
		return errors.New("invalid Responses WebSocket event")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.canInterrupt() {
		// 中断可能在 Next 返回后到达；此时不能再开启并转发一个新输出项。
		return nil
	}
	var response map[string]json.RawMessage
	if json.Unmarshal(event["response"], &response) == nil && response != nil {
		id := responseWebSocketStringField(response, "id")
		if id != "" {
			r.id = id
			r.response = response
			d := r.dispatcher
			d.responsesMu.Lock()
			if previous := d.lastResponse[r.streamID]; previous != id {
				delete(d.responses, previous)
			}
			d.lastResponse[r.streamID] = id
			d.responses[id] = r
			d.responsesMu.Unlock()
		}
	}
	var index int
	_ = json.Unmarshal(event["output_index"], &index)
	switch responseWebSocketStringField(event, "type") {
	case "response.output_item.added":
		if r.open == nil {
			r.open = make(map[int]bool)
		}
		r.open[index] = true
	case "response.output_item.done":
		delete(r.open, index)
		if r.output == nil {
			r.output = make(map[int]json.RawMessage)
		}
		r.output[index] = append(json.RawMessage(nil), event["item"]...)
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "error":
		r.terminal = true
	}
	event["sequence_number"], _ = json.Marshal(r.sequence)
	r.sequence++
	if r.streamID != "" {
		event["stream_id"], _ = json.Marshal(r.streamID)
	}
	if r.canInterrupt() {
		r.cancel()
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return writeResponsesWebSocketMessage(writer, websocket.TextMessage, encoded)
}

func (r *responsesWebSocketResponse) writeInterruption(ctx context.Context, writer *responsesWebSocketWriter, result orchestrator.ChatCompletionResult, transformError responsesWebSocketErrorFunc) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.canInterrupt() || r.id == "" {
		return false, nil
	}
	indices := make([]int, 0, len(r.output))
	for index := range r.output {
		indices = append(indices, index)
	}
	slices.Sort(indices)
	output := make([]json.RawMessage, 0, len(indices))
	for _, index := range indices {
		output = append(output, r.output[index])
	}
	response := cloneResponsesWebSocketPayload(r.response)
	response["status"] = json.RawMessage(`"incomplete"`)
	response["incomplete_details"] = json.RawMessage(`{"reason":"interrupted"}`)
	response["output"], _ = json.Marshal(output)
	body, _ := json.Marshal(response)
	r.terminal = true
	r.cancel()
	event := responsesWebSocketEvent(r.streamID, gin.H{
		"type":            "response.completed",
		"sequence_number": r.sequence,
		"response":        json.RawMessage(body),
	})
	r.sequence++
	if result.RetainResponsesContinuation != nil {
		if err := result.RetainResponsesContinuation(body); err != nil {
			return true, writeResponsesWebSocketError(writer, transformResponsesWebSocketError(ctx, err, transformError), r.streamID)
		}
	}
	return true, writeResponsesWebSocketJSON(writer, event)
}

func (r *responsesWebSocketResponse) finish() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.terminal = true
	r.response = nil
	r.output = nil
	r.open = nil
}
