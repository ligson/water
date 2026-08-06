package realtime

import (
	"context"
	"sync"
)

type Hub struct {
	mu    sync.RWMutex
	subs  map[string]map[chan Message]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: make(map[string]map[chan Message]struct{})}
}

func (h *Hub) Subscribe(ctx context.Context, taskID string) (<-chan Message, func()) {
	ch := make(chan Message, 32)

	h.mu.Lock()
	if _, ok := h.subs[taskID]; !ok {
		h.subs[taskID] = make(map[chan Message]struct{})
	}
	h.subs[taskID][ch] = struct{}{}
	h.mu.Unlock()

	cancel := func() {
		h.mu.Lock()
		if taskSubs, ok := h.subs[taskID]; ok {
			if _, exists := taskSubs[ch]; exists {
				delete(taskSubs, ch)
				close(ch)
			}
			if len(taskSubs) == 0 {
				delete(h.subs, taskID)
			}
		}
		h.mu.Unlock()
	}

	go func() {
		<-ctx.Done()
		cancel()
	}()

	return ch, cancel
}

func (h *Hub) Publish(taskID string, msg Message) {
	h.mu.RLock()
	taskSubs := h.subs[taskID]
	subs := make([]chan Message, 0, len(taskSubs))
	for ch := range taskSubs {
		subs = append(subs, ch)
	}
	h.mu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- msg:
		default:
			h.mu.Lock()
			if taskSubs, ok := h.subs[taskID]; ok {
				if _, exists := taskSubs[ch]; exists {
					delete(taskSubs, ch)
					close(ch)
				}
				if len(taskSubs) == 0 {
					delete(h.subs, taskID)
				}
			}
			h.mu.Unlock()
		}
	}
}
