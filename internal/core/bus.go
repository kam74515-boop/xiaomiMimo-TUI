package core

import (
	"sync"
	"sync/atomic"
)

type subscriber struct {
	ch     chan AgentEvent
	filter map[EventType]bool // nil => receive every event type
}

// Bus is an in-process pub/sub for AgentEvents. The subscriber set is stored
// copy-on-write behind an atomic pointer so Publish — the hot path under the
// message-delta firehose — is lock-free and allocation-free.
type Bus struct {
	mu   sync.Mutex // guards writers (subscribe/unsubscribe) only
	subs atomic.Pointer[[]subscriber]
}

func NewBus() *Bus {
	b := &Bus{}
	empty := []subscriber{}
	b.subs.Store(&empty)
	return b
}

// Subscribe returns a channel that receives every published event.
func (b *Bus) Subscribe(buffer int) <-chan AgentEvent {
	return b.subscribe(buffer, nil)
}

// SubscribeFiltered returns a channel that receives only the given event types,
// keeping low-frequency consumers off the high-rate firehose.
func (b *Bus) SubscribeFiltered(buffer int, types ...EventType) <-chan AgentEvent {
	if len(types) == 0 {
		return b.subscribe(buffer, nil)
	}
	filter := make(map[EventType]bool, len(types))
	for _, t := range types {
		filter[t] = true
	}
	return b.subscribe(buffer, filter)
}

func (b *Bus) subscribe(buffer int, filter map[EventType]bool) <-chan AgentEvent {
	ch := make(chan AgentEvent, buffer)
	b.mu.Lock()
	cur := b.load()
	next := make([]subscriber, len(cur), len(cur)+1)
	copy(next, cur)
	next = append(next, subscriber{ch: ch, filter: filter})
	b.subs.Store(&next)
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes a previously returned channel so its buffer and any
// draining goroutine can be reclaimed. It is a no-op if the channel is unknown.
func (b *Bus) Unsubscribe(ch <-chan AgentEvent) {
	b.mu.Lock()
	cur := b.load()
	next := make([]subscriber, 0, len(cur))
	for _, s := range cur {
		if (<-chan AgentEvent)(s.ch) == ch {
			continue
		}
		next = append(next, s)
	}
	b.subs.Store(&next)
	b.mu.Unlock()
}

func (b *Bus) load() []subscriber {
	if p := b.subs.Load(); p != nil {
		return *p
	}
	return nil
}

func (b *Bus) Publish(event AgentEvent) {
	for _, s := range b.load() {
		if s.filter != nil && !s.filter[event.Type] {
			continue
		}
		select {
		case s.ch <- event:
		default:
		}
	}
}
