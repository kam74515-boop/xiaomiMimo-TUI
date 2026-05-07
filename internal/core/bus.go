package core

import "sync"

type Bus struct {
	mu   sync.RWMutex
	subs []chan AgentEvent
}

func NewBus() *Bus {
	return &Bus{}
}

func (b *Bus) Subscribe(buffer int) <-chan AgentEvent {
	ch := make(chan AgentEvent, buffer)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch
}

func (b *Bus) Publish(event AgentEvent) {
	b.mu.RLock()
	subs := append([]chan AgentEvent(nil), b.subs...)
	b.mu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- event:
		default:
		}
	}
}
