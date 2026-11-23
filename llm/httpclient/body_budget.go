package httpclient

import (
	"context"
	"errors"
	"io"
	"time"

	"golang.org/x/sync/semaphore"
)

const LargeBodyThreshold = 1 << 20

var ErrBodyBudget = errors.New("gateway capacity for in-flight request bodies is exhausted")

type BodyBudget struct {
	limit int64
	room  *semaphore.Weighted
}
type bodyBudgetKey struct{}

func NewBodyBudget(limit int64) *BodyBudget {
	if limit <= 0 {
		return nil
	}
	return &BodyBudget{limit: limit, room: semaphore.NewWeighted(limit)}
}
func (b *BodyBudget) WithContext(ctx context.Context) context.Context {
	if b == nil {
		return ctx
	}
	return context.WithValue(ctx, bodyBudgetKey{}, b)
}
func BodyBudgetFromContext(ctx context.Context) *BodyBudget {
	b, _ := ctx.Value(bodyBudgetKey{}).(*BodyBudget)
	return b
}

// A lease has a single owner. A WS reader hands it to the dispatched turn.
type BodyLease struct {
	budget *BodyBudget
	held   int64
}

func (b *BodyBudget) Admit(ctx context.Context, length int64) (*BodyLease, error) {
	if b == nil {
		return nil, nil
	}
	l := &BodyLease{budget: b}
	if length < LargeBodyThreshold {
		return l, nil
	}
	n := min(b.limit, length)
	if !b.room.TryAcquire(n) {
		wait, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := b.room.Acquire(wait, n); err != nil {
			return nil, ErrBodyBudget
		}
	}
	l.held = n
	return l, nil
}
func (l *BodyLease) Release() {
	if l != nil && l.held > 0 {
		l.budget.room.Release(l.held)
		l.held = 0
	}
}

// Retain transfers body credit from the current turn and the old warmup to a
// replacement warmup. The turn's deferred Release still owns any surplus.
func (l *BodyLease) Retain(previous *BodyLease, size int64) (*BodyLease, error) {
	if l == nil {
		previous.Release()
		return nil, nil
	}
	if err := l.Cover(previous, size); err != nil {
		return nil, err
	}
	if previous != nil {
		l.held += previous.held
		previous.held = 0
	}
	var keep int64
	if size >= LargeBodyThreshold {
		keep = min(size, l.budget.limit)
	}
	l.held -= keep
	return &BodyLease{budget: l.budget, held: keep}, nil
}

// Cover reserves the finished body size, counting credit already held by a
// warmup that will remain alive throughout this turn. Failure changes neither lease.
func (l *BodyLease) Cover(warmup *BodyLease, size int64) error {
	if l == nil || size < LargeBodyThreshold {
		return nil
	}
	needed := min(size, l.budget.limit) - l.held
	if warmup != nil {
		needed -= warmup.held
	}
	if needed <= 0 {
		return nil
	}
	if !l.budget.room.TryAcquire(needed) {
		return ErrBodyBudget
	}
	l.held += needed
	return nil
}

// Unknown lengths and decompression grow in bounded steps. Never wait while
// holding part of the budget: several partial uploads could otherwise deadlock.
func (l *BodyLease) charge(size int64) error {
	if l == nil || size < LargeBodyThreshold || size <= l.held {
		return nil
	}
	const step = 256 << 10
	return l.Cover(nil, (size+step-1)/step*step)
}

type budgetReader struct {
	io.Reader
	lease *BodyLease
	total int64
}

func (l *BodyLease) Reader(r io.Reader, offset int64) io.Reader {
	if l == nil {
		return r
	}
	return &budgetReader{Reader: r, lease: l, total: offset}
}
func (r *budgetReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p[:min(len(p), 64<<10)])
	r.total += int64(n)
	if failure := r.lease.charge(r.total); failure != nil {
		return 0, failure
	}
	return n, err
}
