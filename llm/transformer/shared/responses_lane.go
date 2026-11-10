package shared

import "sync"

type ResponsesBudget struct {
	mu    sync.Mutex
	used  int
	limit int
}

func NewResponsesBudget(limit int) *ResponsesBudget { return &ResponsesBudget{limit: limit} }

func (b *ResponsesBudget) Replace(previous, next int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if next > b.limit-(b.used-previous) {
		return false
	}
	b.used += next - previous
	return true
}

type ResponsesRecord struct {
	ID       string
	Origin   ResponsesOrigin
	Snapshot *ResponsesSnapshot
}

type ResponsesLane struct {
	id     string
	budget *ResponsesBudget
	mu     sync.Mutex
	record ResponsesRecord
	size   int
	closed bool
	leases map[ResponsesOrigin]responsesLaneLease
}

func NewResponsesLane(id string, budget *ResponsesBudget) *ResponsesLane {
	return &ResponsesLane{id: id, budget: budget, leases: make(map[ResponsesOrigin]responsesLaneLease)}
}

func (l *ResponsesLane) ID() string               { return l.id }
func (l *ResponsesLane) Budget() *ResponsesBudget { return l.budget }

func (l *ResponsesLane) Record() ResponsesRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.record
}

func (l *ResponsesLane) Remember(record ResponsesRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	size := record.Snapshot.Size()
	if !l.budget.Replace(l.size, size) {
		record.Snapshot = nil
		size = 0
		l.budget.Replace(l.size, 0)
	}
	l.record, l.size = record, size
}

type responsesLaneLease struct {
	available func(string) bool
	retire    func()
}

// CanContinue checks the original socket without extending its lifetime or dialing.
// Never call transport code while holding the lane lock: eviction takes the reverse path.
func (l *ResponsesLane) CanContinue(record ResponsesRecord) bool {
	l.mu.Lock()
	lease, ok := l.leases[record.Origin]
	valid := !l.closed && l.record.ID == record.ID
	l.mu.Unlock()
	return valid && ok && lease.available(record.ID)
}

func (l *ResponsesLane) RegisterLease(origin ResponsesOrigin, available func(string) bool, retire func()) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return false
	}
	l.leases[origin] = responsesLaneLease{available: available, retire: retire}
	return true
}

func (l *ResponsesLane) ForgetLease(origin ResponsesOrigin) {
	l.mu.Lock()
	delete(l.leases, origin)
	l.mu.Unlock()
}

func (l *ResponsesLane) RetireLeases() {
	l.mu.Lock()
	leases := l.leases
	l.leases = make(map[ResponsesOrigin]responsesLaneLease)
	l.mu.Unlock()
	for _, lease := range leases {
		lease.retire()
	}
}

func (l *ResponsesLane) Close() {
	l.mu.Lock()
	l.closed = true
	l.budget.Replace(l.size, 0)
	l.size = 0
	l.record = ResponsesRecord{}
	l.mu.Unlock()
	l.RetireLeases()
}
