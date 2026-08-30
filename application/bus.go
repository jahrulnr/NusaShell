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
	subs map[int]*busSubscriber
	next int
}

const busNormalQueueLimit = 256

type busSubscriber struct {
	mu              sync.Mutex
	critical        []contracts.Event
	out             chan contracts.Event
	notify          chan struct{}
	done            chan struct{}
	stopped         chan struct{}
	closeOne        sync.Once
	closed          bool
	criticalPending bool
}

func newBusSubscriber() *busSubscriber {
	sub := &busSubscriber{
		out:     make(chan contracts.Event, busNormalQueueLimit),
		notify:  make(chan struct{}, 1),
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go sub.deliver()
	return sub
}

func (s *busSubscriber) publish(ev contracts.Event) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if !isCriticalBusEvent(ev.Type) && s.criticalPending {
		s.mu.Unlock()
		return
	}
	if !isCriticalBusEvent(ev.Type) {
		select {
		case s.out <- ev:
		default:
		}
		s.mu.Unlock()
		return
	}
	if !s.criticalPending {
		select {
		case s.out <- ev:
			s.mu.Unlock()
			return
		default:
		}
	}
	s.critical = append(s.critical, ev)
	s.criticalPending = true
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *busSubscriber) deliver() {
	defer close(s.stopped)
	for {
		s.mu.Lock()
		if len(s.critical) == 0 {
			s.criticalPending = false
			s.mu.Unlock()
			select {
			case <-s.done:
				return
			case <-s.notify:
			}
			continue
		}
		ev := s.critical[0]
		s.critical[0] = contracts.Event{}
		s.critical = s.critical[1:]
		s.mu.Unlock()
		select {
		case s.out <- ev:
		case <-s.done:
			return
		}
	}
}

func (s *busSubscriber) close() {
	s.closeOne.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.done)
		<-s.stopped
		close(s.out)
	})
}

func isCriticalBusEvent(typ string) bool {
	switch typ {
	case contracts.EventTurnStarted,
		contracts.EventTurnDone,
		contracts.EventTurnError,
		contracts.EventToolCompleted,
		contracts.EventSteerQueued,
		contracts.EventSteerApplied,
		contracts.EventSteerCancelled,
		contracts.EventAskPending,
		contracts.EventAskAnswered,
		contracts.EventAskCancelled,
		contracts.EventCompacting,
		contracts.EventCompacted,
		contracts.EventCompactionFailed,
		contracts.EventAutoContinue,
		contracts.EventAcpRunDone,
		contracts.EventAcpPermissionRequested,
		contracts.EventAcpPermissionDecided:
		return true
	default:
		return false
	}
}

func NewBus() *Bus {
	return &Bus{subs: map[int]*busSubscriber{}}
}

// Subscribe registers a buffered delivery stream; returns an unsubscribe func.
func (b *Bus) Subscribe() (int, <-chan contracts.Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	sub := newBusSubscriber()
	b.subs[id] = sub
	return id, sub.out, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if sub, ok := b.subs[id]; ok {
			delete(b.subs, id)
			sub.close()
		}
	}
}

// Publish sends an event to every subscriber. High-volume events are bounded
// and may be dropped for slow consumers; turn terminal events remain queued.
func (b *Bus) Publish(ev contracts.Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, sub := range b.subs {
		sub.publish(ev)
	}
}

func (b *Bus) Emit(typ string, v any) {
	b.Publish(contracts.NewEvent(typ, v))
}
