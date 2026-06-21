package core

import "sync"

type subscriber struct {
	ch     chan AgentEvent
	filter map[EventType]bool // nil => receive every event type
}

type Bus struct {
	mu   sync.RWMutex
	subs []subscriber
}

func NewBus() *Bus {
	return &Bus{}
}

// Subscribe returns a channel that receives every published event.
func (b *Bus) Subscribe(buffer int) <-chan AgentEvent {
	return b.subscribe(buffer, nil)
}

// SubscribeFiltered returns a channel that receives only the given event types.
// This keeps low-frequency control consumers (goal/memory) off the high-rate
// message-delta firehose so their bounded buffers cannot be flooded and drop
// events.
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
	b.subs = append(b.subs, subscriber{ch: ch, filter: filter})
	b.mu.Unlock()
	return ch
}

func (b *Bus) Publish(event AgentEvent) {
	b.mu.RLock()
	subs := append([]subscriber(nil), b.subs...)
	b.mu.RUnlock()
	for _, s := range subs {
		if s.filter != nil && !s.filter[event.Type] {
			continue
		}
		select {
		case s.ch <- event:
		default:
		}
	}
}
