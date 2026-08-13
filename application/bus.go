package application

import (
	"sync"

	"nusashell/contracts"
)

// Bus fans application events out to subscribers (transport layer). It is
// deliberately not a message queue: no persistence, no ordering guarantees
// beyond per-subscriber delivery order.
type Bus struct {
	mu   sync.RWMutex
	subs map[int]chan contracts.Event
	next int
}

func NewBus() *Bus {
	return &Bus{subs: map[int]chan contracts.Event{}}
}

// Subscribe registers a buffered channel; returns an unsubscribe func.
func (b *Bus) Subscribe() (int, <-chan contracts.Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	ch := make(chan contracts.Event, 256)
	b.subs[id] = ch
	return id, ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if c, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(c)
		}
	}
}

// Publish sends an event to every subscriber, dropping it when a subscriber
// channel is full so a slow consumer cannot stall the agent loop.
func (b *Bus) Publish(ev contracts.Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (b *Bus) Emit(typ string, v any) {
	b.Publish(contracts.NewEvent(typ, v))
}
