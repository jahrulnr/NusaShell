package whatsapp

import (
	"context"
	"errors"
	"sync"
	"time"
)

// pacer queues outbound messages per chat so two sends to the same chat
// keep a human-like least gap (Settings.SendPacing). Messages wait in FIFO
// order and nothing is dropped for pacing: the only refusals are a full
// queue (Settings.QueueCap) and a caller that is already gone. The queue
// lives in memory; a stop fails what still waits.
type pacer struct {
	mu      sync.Mutex
	queues  map[string]*chatQueue
	stop    chan struct{}
	stopped bool
}

type chatQueue struct {
	tasks    []*sendTask
	lastSent time.Time
	running  bool
	idle     chan struct{} // closed when a task arrives; made again when drained
}

// sendTask is one queued send. started is set under the pacer's lock just
// before run, so a waiter whose context ends can tell a queued send from
// one in flight.
type sendTask struct {
	ctx     context.Context
	run     func(ctx context.Context) error
	done    chan error
	started bool
}

var (
	errQueueFull    = errors.New("the WhatsApp send queue for this chat is full")
	errPacerStopped = errors.New("WhatsApp stopped before the message was sent")
)

const (
	pacerIdleGrace = 2 * time.Second
	pacedSendLimit = 20 * time.Second
)

func newPacer() *pacer {
	return &pacer{queues: map[string]*chatQueue{}, stop: make(chan struct{})}
}

// paced runs run as a paced send to one chat: it waits for the chat's
// turn and the least gap, then runs. The caller's context bounds the
// wait. A send already in flight when the caller gives up is the
// worker's: the caller hears success rather than sending the message
// again, and the worker logs the real outcome.
func (c *Channel) paced(ctx context.Context, chat string, run func(ctx context.Context) error) error {
	p := c.pacer
	task := &sendTask{ctx: ctx, run: run, done: make(chan error, 1)}
	// Settings are read before the pacer's lock: Stop takes c.mu first.
	queueCap := c.current().QueueCap
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return errPacerStopped
	}
	q, ok := p.queues[chat]
	if !ok {
		q = &chatQueue{idle: make(chan struct{})}
		p.queues[chat] = q
	}
	if len(q.tasks) >= queueCap {
		p.mu.Unlock()
		return errQueueFull
	}
	q.tasks = append(q.tasks, task)
	spawn := !q.running
	q.running = true
	if q.idle != nil {
		close(q.idle)
		q.idle = nil
	}
	p.mu.Unlock()
	if spawn {
		go c.pacerWorker(chat, q)
	}
	select {
	case err := <-task.done:
		return err
	case <-ctx.Done():
		p.mu.Lock()
		started := task.started
		p.mu.Unlock()
		if !started {
			return ctx.Err()
		}
		return nil
	}
}

// pacerWorker drains one chat's queue in order, keeping the gap.
func (c *Channel) pacerWorker(chat string, q *chatQueue) {
	p := c.pacer
	for {
		gap := time.Duration(c.current().SendPacing) * time.Second
		p.mu.Lock()
		if p.stopped {
			p.mu.Unlock()
			return // stop failed every queued task
		}
		if len(q.tasks) == 0 {
			if q.idle == nil {
				q.idle = make(chan struct{})
			}
			idle := q.idle
			p.mu.Unlock()
			select {
			case <-idle:
				continue
			case <-p.stop:
				return
			case <-time.After(pacerIdleGrace):
			}
			p.mu.Lock()
			if len(q.tasks) == 0 {
				q.running = false
				p.mu.Unlock()
				return
			}
			p.mu.Unlock()
			continue
		}
		task := q.tasks[0]
		q.tasks = q.tasks[1:]
		wait := time.Until(q.lastSent.Add(gap))
		p.mu.Unlock()
		if wait > 0 {
			select {
			case <-time.After(wait):
			case <-p.stop:
				task.done <- errPacerStopped
				return
			}
		}
		if err := task.ctx.Err(); err != nil {
			task.done <- err
			continue
		}
		p.mu.Lock()
		task.started = true
		p.mu.Unlock()
		// The send's context is the worker's own: a caller giving up does
		// not cut a message off half sent.
		sendCtx, cancel := context.WithTimeout(context.Background(), pacedSendLimit)
		err := task.run(sendCtx)
		cancel()
		if err == nil {
			p.mu.Lock()
			q.lastSent = time.Now()
			p.mu.Unlock()
		} else {
			c.log.Warn("a paced WhatsApp send failed", "chat", chat, "error", err)
		}
		task.done <- err
	}
}

// stopPacer fails every queued send and refuses new ones.
func (c *Channel) stopPacer() {
	p := c.pacer
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	p.stopped = true
	close(p.stop)
	for _, q := range p.queues {
		for _, task := range q.tasks {
			task.done <- errPacerStopped
		}
		q.tasks = nil
	}
}
