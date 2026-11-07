package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

const (
	responsesSessionTTL         = 2 * time.Hour
	responsesSessionMaxRecords  = 2048
	responsesSessionMaxBytes    = 64 << 20
	responsesSessionMaxResponse = 16 << 20
)

type responsesSessionStore struct {
	mu         sync.Mutex
	byResponse map[responsesSessionKey]*responsesSessionRecord
	totalBytes int
	load       responsesSessionLoadFunc
	save       func(context.Context, []byte, []byte)
	known      func(context.Context, string) (bool, error)
}

type responsesSessionLoadFunc func(ctx context.Context, responseID string) (requestBody, responseBody []byte, found bool, err error)

type responsesSessionKey struct {
	scope      string
	responseID string
}

type responsesSessionRecord struct {
	input     []json.RawMessage
	output    []json.RawMessage
	sessionID string
	updatedAt time.Time
	size      int
	complete  bool
	origin    shared.ResponsesOrigin
}

func newResponsesSessionStore(loaders ...responsesSessionLoadFunc) *responsesSessionStore {
	store := new(responsesSessionStore)
	store.byResponse = make(map[responsesSessionKey]*responsesSessionRecord)
	if len(loaders) > 0 {
		store.load = loaders[0]
	}
	return store
}

func (s *responsesSessionStore) prepare(ctx context.Context, body []byte) ([]byte, string, error) {
	sessionID, _ := shared.GetSessionID(ctx)
	var current map[string]json.RawMessage
	if json.Unmarshal(body, &current) != nil || current == nil {
		// Invalid request bodies are validated by the normal request parser;
		// session restoration must pass them through without changing the error.
		//nolint:nilerr
		return body, responseSessionID(sessionID), nil
	}

	normalizedInput, inputChanged := normalizeResponseSessionInput(current["input"])
	if inputChanged {
		current["input"] = normalizedInput
	}

	previousID := responseSessionString(current["previous_response_id"])
	if previousID == "" {
		return encodePreparedResponsesBody(body, current, inputChanged), responseSessionID(sessionID), nil
	}

	record, err := s.lookupOrLoad(ctx, previousID)
	if err != nil {
		return encodePreparedResponsesBody(body, current, inputChanged), responseSessionID(sessionID), err
	}
	if record == nil || !record.complete {
		return encodePreparedResponsesBody(body, current, inputChanged), responseSessionID(sessionID), nil
	}
	if record.sessionID != "" {
		sessionID = record.sessionID
	}
	sessionID = responseSessionID(sessionID)

	merged := cloneResponseSessionPayload(current)

	baseItems, _ := normalizeResponseSessionItems(record.input)
	normalizedOutput, _ := normalizeResponseSessionItems(record.output)
	baseItems = append(baseItems, normalizedOutput...)
	baseItems = append(baseItems, responseSessionInputItems(current["input"])...)
	if len(baseItems) > 0 {
		encoded, err := json.Marshal(baseItems)
		if err != nil {
			return body, sessionID, err
		}
		merged["input"] = encoded
	} else {
		delete(merged, "input")
	}
	delete(merged, "previous_response_id")

	encoded, err := json.Marshal(merged)
	if err != nil {
		return body, sessionID, err
	}

	return encoded, sessionID, nil
}

func (s *responsesSessionStore) lookupOrLoad(ctx context.Context, responseID string) (*responsesSessionRecord, error) {
	if scope, ok := shared.GetSessionScope(ctx); !ok || strings.TrimSpace(scope) == "" {
		return nil, nil
	}
	type turn struct {
		record *responsesSessionRecord
	}
	metadata := s.lookup(ctx, responseID)
	var turns []turn
	visited := make(map[string]bool)
	var base []json.RawMessage
	sessionID, _ := shared.GetSessionID(ctx)
	for id := responseID; id != ""; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if visited[id] {
			return nil, nil
		}
		visited[id] = true
		if cached := s.lookup(ctx, id); cached != nil && cached.complete {
			if len(turns) == 0 {
				return cached, nil
			}
			base = append(cached.input, cached.output...)
			if cached.sessionID != "" {
				sessionID = cached.sessionID
			}
			break
		}
		if s.load == nil {
			return metadata, nil
		}
		requestBody, responseBody, found, err := s.load(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("restore Responses history: %w", err)
		}
		if !found {
			return metadata, nil
		}
		recordID, previous, record := decodeResponseSessionRecord(requestBody, responseBody)
		if record == nil || recordID != id {
			return nil, nil
		}
		turns = append(turns, turn{record})
		id = previous
	}
	// Build once from the oldest known complete ancestor. Cache admission is optional.
	for i, turn := range slices.Backward(turns) {
		base = append(base, turn.record.input...)
		if i > 0 {
			base = append(base, turn.record.output...)
		}
	}
	record := turns[0].record
	record.complete = true
	if metadata != nil {
		record.origin = metadata.origin
		if metadata.sessionID != "" {
			sessionID = metadata.sessionID
		}
	}
	record.input = base
	record.sessionID = sessionID
	record.size = rawResponseSessionValuesSize(record.input) + rawResponseSessionValuesSize(record.output)
	s.cacheRecord(ctx, responseID, record)
	return record, nil
}

func encodePreparedResponsesBody(original []byte, payload map[string]json.RawMessage, changed bool) []byte {
	if !changed {
		return original
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return original
	}
	return encoded
}

// decodeResponseSessionRecord does not impose a cache-size limit on usable history.
func decodeResponseSessionRecord(requestBody, responseBody []byte) (string, string, *responsesSessionRecord) {
	var request map[string]json.RawMessage
	var response struct {
		ID     string            `json:"id"`
		Status string            `json:"status"`
		Output []json.RawMessage `json:"output"`
	}
	if json.Unmarshal(requestBody, &request) != nil || request == nil || json.Unmarshal(responseBody, &response) != nil || response.ID == "" {
		return "", "", nil
	}
	//nolint:misspell
	if response.Status == "failed" || response.Status == "cancelled" || response.Status == "canceled" {
		return "", "", nil
	}
	input := responseSessionInputItems(request["input"])
	if value := bytes.TrimSpace(request["input"]); len(value) > 0 && !bytes.Equal(value, []byte("null")) && input == nil {
		return "", "", nil
	}
	previous := responseSessionString(request["previous_response_id"])
	if value := bytes.TrimSpace(request["previous_response_id"]); len(value) > 0 && !bytes.Equal(value, []byte("null")) {
		var id string
		if json.Unmarshal(value, &id) != nil {
			return "", "", nil
		}
	}
	return response.ID, previous, &responsesSessionRecord{input: input, output: cloneResponseSessionValues(response.Output), size: rawResponseSessionValuesSize(input) + rawResponseSessionValuesSize(response.Output)}
}

func (s *responsesSessionStore) record(ctx context.Context, requestBody, responseBody []byte) {
	if s.save != nil {
		s.save(ctx, requestBody, responseBody)
	}
	if !json.Valid(requestBody) || !json.Valid(responseBody) {
		return
	}
	id := gjson.GetBytes(responseBody, "id").String()
	status := gjson.GetBytes(responseBody, "status").String()
	if id == "" || status == "failed" || status == "cancelled" || status == "canceled" {
		return
	}
	record := &responsesSessionRecord{}
	if len(responseBody) <= responsesSessionMaxResponse && len(gjson.GetBytes(requestBody, "input").Raw) <= responsesSessionMaxResponse+2 {
		_, previous, decoded := decodeResponseSessionRecord(requestBody, responseBody)
		if decoded != nil && previous == "" {
			record = decoded
			record.complete = true
		}
	}
	if attempt := shared.GetResponsesAttempt(ctx); attempt != nil {
		record.origin = attempt.Origin()
	}
	record.sessionID, _ = shared.GetSessionID(ctx)
	s.cacheRecord(ctx, id, record)
}

// 取消生成后仍须保存客户端已收到的完整输出项，取消信号不应取消这次短暂的历史写入。
func (s *responsesSessionStore) continuationRecorder(ctx context.Context, requestBody []byte) func([]byte) {
	return func(responseBody []byte) {
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		s.record(persistCtx, requestBody, responseBody)
	}
}

func (s *responsesSessionStore) cacheRecord(ctx context.Context, id string, record *responsesSessionRecord) {
	scope, ok := shared.GetSessionScope(ctx)
	if !ok || strings.TrimSpace(scope) == "" {
		return
	}
	now := time.Now()
	key := responsesSessionKey{scope: scope, responseID: id}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictExpiredLocked(now)
	if previous := s.byResponse[key]; previous != nil {
		s.totalBytes -= previous.size
	}
	copy := *record
	if copy.size > responsesSessionMaxResponse {
		copy.input, copy.output, copy.size, copy.complete = nil, nil, 0, false
	}
	copy.input = cloneResponseSessionValues(copy.input)
	copy.output = cloneResponseSessionValues(copy.output)
	copy.updatedAt = now
	s.byResponse[key] = &copy
	s.totalBytes += copy.size
	for s.totalBytes > responsesSessionMaxBytes {
		var oldest *responsesSessionRecord
		for _, item := range s.byResponse {
			if item.size > 0 && (oldest == nil || item.updatedAt.Before(oldest.updatedAt)) {
				oldest = item
			}
		}
		if oldest == nil {
			break
		}
		s.totalBytes -= oldest.size
		oldest.input, oldest.output, oldest.size, oldest.complete = nil, nil, 0, false
	}
	for len(s.byResponse) > responsesSessionMaxRecords {
		if !s.evictOldestLocked() {
			s.totalBytes = 0
			break
		}
	}
}

func (s *responsesSessionStore) lookup(ctx context.Context, responseID string) *responsesSessionRecord {
	now := time.Now()
	scope, ok := shared.GetSessionScope(ctx)
	if !ok || strings.TrimSpace(scope) == "" {
		return nil
	}
	key := responsesSessionKey{scope: scope, responseID: responseID}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictExpiredLocked(now)
	record := s.byResponse[key]
	if record == nil {
		return nil
	}

	return &responsesSessionRecord{
		input:     cloneResponseSessionValues(record.input),
		output:    cloneResponseSessionValues(record.output),
		sessionID: record.sessionID,
		updatedAt: record.updatedAt,
		size:      record.size,
		complete:  record.complete,
		origin:    record.origin,
	}
}

func (s *responsesSessionStore) evictExpiredLocked(now time.Time) {
	for key, record := range s.byResponse {
		if record == nil || now.Sub(record.updatedAt) > responsesSessionTTL {
			if record != nil {
				s.totalBytes -= record.size
			}
			delete(s.byResponse, key)
		}
	}
}

func (s *responsesSessionStore) evictOldestLocked() bool {
	var oldestKey responsesSessionKey
	var oldest time.Time
	found := false
	for key, record := range s.byResponse {
		if record == nil {
			oldestKey = key
			found = true
			break
		}
		if !found || record.updatedAt.Before(oldest) {
			oldestKey = key
			oldest = record.updatedAt
			found = true
		}
	}
	if !found {
		return false
	}
	if record := s.byResponse[oldestKey]; record != nil {
		s.totalBytes -= record.size
	}
	delete(s.byResponse, oldestKey)
	return true
}

func (s *responsesSessionStore) wrapStream(
	ctx context.Context,
	requestBody []byte,
	stream streams.Stream[*httpclient.StreamEvent],
) streams.Stream[*httpclient.StreamEvent] {
	responseHeaders := httpclient.GetResponseHeaders(stream)
	wrapped := new(responsesSessionStream)
	wrapped.ctx = ctx
	wrapped.store = s
	wrapped.requestBody = append([]byte(nil), requestBody...)
	wrapped.inner = stream

	return httpclient.WithResponseHeaders(wrapped, responseHeaders)
}

type responsesSessionStream struct {
	ctx         context.Context
	store       *responsesSessionStore
	requestBody []byte
	inner       streams.Stream[*httpclient.StreamEvent]
	current     *httpclient.StreamEvent
	chunks      []*httpclient.StreamEvent
	chunksBytes int
	terminal    bool
	recorded    bool
	overflow    bool
}

func (s *responsesSessionStream) AcknowledgeDelivery() {
	if observer, ok := s.inner.(streams.DeliveryAcknowledger); ok {
		observer.AcknowledgeDelivery()
	}
}

func (s *responsesSessionStream) DeliveryAcknowledged() bool {
	observer, ok := s.inner.(streams.DeliveryAcknowledger)
	return ok && observer.DeliveryAcknowledged()
}

func (s *responsesSessionStream) WaitForFinalization() {
	if waiter, ok := s.inner.(streams.FinalizationWaiter); ok {
		waiter.WaitForFinalization()
	}
}

func (s *responsesSessionStream) Deliver(ctx context.Context, write func() (bool, error)) error {
	boundary, ok := s.inner.(streams.DeliveryBoundary)
	if ok {
		return boundary.Deliver(ctx, write)
	}
	terminal, err := write()
	if err == nil && terminal {
		s.AcknowledgeDelivery()
	}
	return err
}

func (s *responsesSessionStream) ExpectedStreamChoices() int {
	choices, ok := s.inner.(interface{ ExpectedStreamChoices() int })
	if !ok {
		return 0
	}
	return choices.ExpectedStreamChoices()
}

func (s *responsesSessionStream) Next() bool {
	if !s.inner.Next() {
		s.current = nil
		return false
	}

	s.current = s.inner.Current()
	if s.current != nil {
		if !s.overflow {
			s.chunksBytes += len(s.current.Type) + len(s.current.LastEventID) + len(s.current.Data)
			if s.chunksBytes > responsesSessionMaxResponse {
				s.chunks = nil
				s.overflow = true
			} else {
				s.chunks = append(s.chunks, &httpclient.StreamEvent{
					Type:        s.current.Type,
					LastEventID: s.current.LastEventID,
					Data:        append([]byte(nil), s.current.Data...),
					Size:        s.current.Size,
				})
			}
		}
		if IsTerminalStreamEvent(s.current) {
			s.terminal = true
			// A final Responses event carries the complete output even when the
			// bounded delta buffer was exceeded. Persist continuation separately.
			var terminal struct {
				Response json.RawMessage `json:"response"`
			}
			if s.overflow && json.Unmarshal(s.current.Data, &terminal) == nil && len(terminal.Response) > 0 {
				var complete struct {
					Output json.RawMessage `json:"output"`
				}
				if json.Unmarshal(terminal.Response, &complete) == nil && len(complete.Output) > 0 {
					s.store.record(s.ctx, s.requestBody, terminal.Response)
					s.recorded = true
				}
			}
			s.recordCompleted()
		}
	}

	return true
}

func (s *responsesSessionStream) Current() *httpclient.StreamEvent { return s.current }

func (s *responsesSessionStream) Err() error { return s.inner.Err() }

func (s *responsesSessionStream) Close() error {
	s.recordCompleted()

	return s.inner.Close()
}

func (s *responsesSessionStream) recordCompleted() {
	if s.recorded || s.overflow || !s.terminal {
		return
	}

	body, _, err := responses.AggregateStreamChunks(s.ctx, s.chunks)
	if err != nil {
		return
	}

	s.store.record(s.ctx, s.requestBody, body)
	s.recorded = true
}

func cloneResponseSessionPayload(payload map[string]json.RawMessage) map[string]json.RawMessage {
	cloned := make(map[string]json.RawMessage, len(payload))
	for key, value := range payload {
		cloned[key] = cloneResponseSessionValue(value)
	}
	return cloned
}

func cloneResponseSessionValue(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func cloneResponseSessionValues(values []json.RawMessage) []json.RawMessage {
	cloned := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		cloned = append(cloned, cloneResponseSessionValue(value))
	}
	return cloned
}

func rawResponseSessionValuesSize(values []json.RawMessage) int {
	total := 0
	for _, value := range values {
		total += len(value)
	}
	return total
}

func responseSessionID(sessionID string) string {
	if sessionID != "" {
		return sessionID
	}
	return "responses-" + uuid.NewString()
}

func responseSessionString(value json.RawMessage) string {
	var result string
	_ = json.Unmarshal(value, &result)
	return result
}

func responseSessionInputItems(value json.RawMessage) []json.RawMessage {
	if len(value) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nil
	}

	var items []json.RawMessage
	if json.Unmarshal(value, &items) == nil {
		return cloneResponseSessionValues(items)
	}

	var text string
	if json.Unmarshal(value, &text) != nil {
		return nil
	}
	item, err := json.Marshal(map[string]any{
		"type":    "message",
		"role":    "user",
		"content": text,
	})
	if err != nil {
		return nil
	}
	return []json.RawMessage{item}
}

func normalizeResponseSessionInput(value json.RawMessage) (json.RawMessage, bool) {
	var items []json.RawMessage
	if json.Unmarshal(value, &items) != nil {
		return value, false
	}

	normalized, changed := normalizeResponseSessionItems(items)
	if !changed {
		return value, false
	}

	encoded, err := json.Marshal(normalized)
	if err != nil {
		return value, false
	}
	return encoded, true
}

func normalizeResponseSessionItems(items []json.RawMessage) ([]json.RawMessage, bool) {
	normalized := make([]json.RawMessage, 0, len(items))
	changed := false
	for _, item := range items {
		var object map[string]json.RawMessage
		if json.Unmarshal(item, &object) != nil || object == nil {
			normalized = append(normalized, cloneResponseSessionValue(item))
			continue
		}
		if _, ok := object["status"]; !ok {
			normalized = append(normalized, cloneResponseSessionValue(item))
			continue
		}
		if responseSessionString(object["type"]) == "web_search_call" {
			normalized = append(normalized, cloneResponseSessionValue(item))
			continue
		}

		delete(object, "status")
		encoded, err := json.Marshal(object)
		if err != nil {
			normalized = append(normalized, cloneResponseSessionValue(item))
			continue
		}
		normalized = append(normalized, encoded)
		changed = true
	}
	return normalized, changed
}
