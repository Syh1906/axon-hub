package biz

import "sync"

type consumptionWait struct {
	done    chan struct{}
	readers int
}

type consumptionNotifications struct {
	mu      sync.Mutex
	waiting map[string]*consumptionWait
}

func (n *consumptionNotifications) subscribe(key string) (<-chan struct{}, func()) {
	n.mu.Lock()
	w := n.waiting[key]
	if w == nil {
		w = &consumptionWait{done: make(chan struct{})}
		n.waiting[key] = w
	}
	w.readers++
	n.mu.Unlock()
	return w.done, func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		w.readers--
		if w.readers == 0 && n.waiting[key] == w {
			delete(n.waiting, key)
		}
	}
}

func (n *consumptionNotifications) published(key string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if w := n.waiting[key]; w != nil {
		delete(n.waiting, key)
		close(w.done)
	}
}
