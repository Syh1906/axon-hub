package shared

import "encoding/json"

// ResponsesSnapshot is an immutable, complete chain of input and completed output
// items. Native WS turns append one node; history is flattened only for recovery.
// Callers transfer ownership of input/output and must not mutate their bytes.
type ResponsesSnapshot struct {
	Input  []json.RawMessage
	Output []json.RawMessage
	parent *ResponsesSnapshot
	bytes  int
	items  int
	turns  int
}

func NewResponsesSnapshot(parent *ResponsesSnapshot, input, output []json.RawMessage) *ResponsesSnapshot {
	// Charge node/slice bookkeeping as well as JSON bytes, so empty turns also
	// consume the existing bounded connection budget.
	s := &ResponsesSnapshot{parent: parent, Input: input, Output: output, bytes: 128, turns: 1}
	if parent != nil {
		s.bytes += parent.bytes
		s.items = parent.items
		s.turns += parent.turns
	}
	for _, items := range [][]json.RawMessage{input, output} {
		s.items += len(items)
		s.bytes += 24 * len(items)
		for _, item := range items {
			s.bytes += len(item)
		}
	}
	return s
}

func (s *ResponsesSnapshot) Size() int {
	if s == nil {
		return 0
	}
	return s.bytes
}

// Items performs one linear traversal without copying any JSON item bytes.
func (s *ResponsesSnapshot) Items() []json.RawMessage {
	if s == nil {
		return nil
	}
	turns := make([]*ResponsesSnapshot, 0, s.turns)
	for turn := s; turn != nil; turn = turn.parent {
		turns = append(turns, turn)
	}
	items := make([]json.RawMessage, 0, s.items)
	for i := len(turns) - 1; i >= 0; i-- {
		items = append(items, turns[i].Input...)
		items = append(items, turns[i].Output...)
	}
	return items
}
