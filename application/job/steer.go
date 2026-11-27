package job

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// ErrMailboxFull answers a submission the mailbox has no room for. Nothing was
// buffered: the caller reports the refusal instead of losing the input.
var (
	ErrMailboxFull = errors.New("steering mailbox full")
	// ErrRelayRecordMissing refuses a relayed input whose accepting room did not
	// write its provenance line first.
	ErrRelayRecordMissing = errors.New("relayed input has no acceptance record")
)

// SteeringState is the outcome slot one submitted input occupies.
// Pending input still waits in the mailbox and can be recalled; claimed
// input was drained by the turn loop but its user line has not committed
// yet; persisted input is in history; recalled input was withdrawn
// before claim.
type SteeringState string

const (
	SteeringPending   SteeringState = "pending"
	SteeringClaimed   SteeringState = "claimed"
	SteeringPersisted SteeringState = "persisted"
	SteeringRecalled  SteeringState = "recalled"
)

// SteeringReceipt tracks one submitted input through claim and
// persistence. Transitions are driven by the owning mailbox under its
// lock, or by the turn loop after claim — never the reverse direction —
// so State reads stay lock-free.
type SteeringReceipt struct {
	state atomic.Value // SteeringState
}

func newSteeringReceipt() *SteeringReceipt {
	r := &SteeringReceipt{}
	r.state.Store(SteeringPending)
	return r
}

// State reports the input's last recorded outcome. A receipt the turn
// loop claimed but never committed stays claimed — the caller reads
// that as "unconsumed" rather than guessing. A zero-value receipt
// holds no recorded transition; it reports pending — benign, and
// Recall still rejects it (nothing buffered matches it).
func (r *SteeringReceipt) State() SteeringState {
	if s, ok := r.state.Load().(SteeringState); ok {
		return s
	}
	return SteeringPending
}

// markPersisted flips a claimed receipt once its user line committed —
// AppendMessage's bool decides, even when it also returned an error.
func (r *SteeringReceipt) markPersisted() {
	r.state.Store(SteeringPersisted)
}

// markRecalled flips a pending receipt withdrawn before claim.
func (r *SteeringReceipt) markRecalled() {
	r.state.Store(SteeringRecalled)
}

// markClaimed flips a pending receipt drained by the turn loop.
func (r *SteeringReceipt) markClaimed() {
	r.state.Store(SteeringClaimed)
}

// steeringEntry is one buffered input — text with its optional
// selections, uploads and page marks — plus the optional receipt
// tracking its outcome (receipt-free on the CLI's push path).
//
// record is the line already written for this input when the room's accept path
// persisted it (an agent room's provenance row). A turn that later delivers the
// same input must USE that line: one accepted message is one line, so nothing
// downstream may write a second one for the same words.
type steeringEntry struct {
	input   valueobject.UserInput
	receipt *SteeringReceipt
	record  *entity.Message
}

// SteeringInput is the exact-turn mid-turn input mailbox shared by the
// CLI's out-of-band steer path and transports that bind one mailbox per
// admitted turn (SendOptions.Steering). Entries carry text plus an
// optional receipt; the turn loop claims pending entries atomically at
// inference safe points — after a tool result persisted, before the
// next provider call — then marks each persisted only once its user
// line commits. The lock never covers I/O or callbacks: claim is a
// state flip under the lock, persistence writes and event emission
// happen after it releases.
type SteeringInput struct {
	mu      sync.Mutex
	pending []steeringEntry
	// signal wakes a turn that can deliver input as it arrives (an
	// agent that takes steering mid-turn).
	signal chan struct{}
}

// NewSteeringInput creates an empty mailbox.
func NewSteeringInput() *SteeringInput { return &SteeringInput{} }

// available reports whether one more input fits the mailbox right now. It is a
// preflight for accept paths that must not write before a refusal; SubmitBefore
// still enforces the same cap atomically.
func (s *SteeringInput) available(limit int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return limit <= 0 || len(s.pending) < limit
}

// Submit buffers input — text and its optional selected references,
// uploads and annotations — and returns the receipt that tracks its
// outcome.
func (s *SteeringInput) Submit(input valueobject.UserInput) *SteeringReceipt {
	r, _ := s.SubmitBefore(input, 0, nil)
	return r
}

// SubmitBefore is Submit with two bounds the relay path needs. A limit above
// zero refuses the submission once that many inputs already wait, without
// buffering anything. record is the acceptance line the room already wrote for
// this input: it travels with the entry from the start, so no fold can meet a
// relayed input before its provenance. A relayed input without that line is a
// broken acceptance and is refused rather than delivered unrecorded.
func (s *SteeringInput) SubmitBefore(input valueobject.UserInput, limit int, record *entity.Message) (*SteeringReceipt, error) {
	if input.Relay != nil && record == nil {
		return nil, ErrRelayRecordMissing
	}
	r := newSteeringReceipt()
	s.mu.Lock()
	if limit > 0 && len(s.pending) >= limit {
		s.mu.Unlock()
		return nil, ErrMailboxFull
	}
	s.pending = append(s.pending, steeringEntry{input: input, receipt: r, record: record})
	s.mu.Unlock()
	s.notify()
	return r, nil
}

// drainPending detaches the inputs no turn consumed — read at the end of a turn
// so the caller can persist what would otherwise be lost.
func (s *SteeringInput) drainPending() []steeringEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.pending
	s.pending = nil
	for _, e := range out {
		if e.receipt != nil {
			e.receipt.markRecalled()
		}
	}
	return out
}

// push buffers receipt-free input — the worker's out-of-band Steer path,
// which carries text only.
func (s *SteeringInput) push(text string) {
	s.mu.Lock()
	s.pending = append(s.pending, steeringEntry{input: valueobject.UserInput{Text: text}})
	s.mu.Unlock()
	s.notify()
}

// arrived fires when input is buffered; one signal may cover several.
func (s *SteeringInput) arrived() <-chan struct{} { return s.channel() }

func (s *SteeringInput) notify() {
	select {
	case s.channel() <- struct{}{}:
	default:
	}
}

func (s *SteeringInput) channel() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.signal == nil {
		s.signal = make(chan struct{}, 1)
	}
	return s.signal
}

// requeue returns claimed entries the turn could not deliver, ahead of
// anything newer, with their receipts pending again.
func (s *SteeringInput) requeue(entries []steeringEntry) {
	if len(entries) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range entries {
		if e.receipt != nil {
			e.receipt.state.Store(SteeringPending)
		}
	}
	s.pending = append(append([]steeringEntry(nil), entries...), s.pending...)
}

// RecallID withdraws the pending input submitted under inputID. Empty
// ids never match: a text-only push has none, and recalling "the newest"
// is a different operation. ok=false — unknown, claimed, persisted, or
// already recalled. Atomic against claim, same as Recall.
func (s *SteeringInput) RecallID(inputID string) bool {
	if inputID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.pending {
		if e.input.InputID != inputID || e.receipt == nil || e.receipt.State() != SteeringPending {
			continue
		}
		s.pending = append(s.pending[:i], s.pending[i+1:]...)
		e.receipt.markRecalled()
		return true
	}
	return false
}

// Recall withdraws one exact pending input — atomic against claim, so a
// receipt already drained or persisted can never be taken back.
// ok=false — unknown, claimed, persisted, or already recalled.
func (s *SteeringInput) Recall(r *SteeringReceipt) bool {
	if r == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.State() != SteeringPending {
		return false
	}
	for i, e := range s.pending {
		if e.receipt == r {
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			e.receipt.markRecalled()
			return true
		}
	}
	return false
}

// popLast withdraws the newest pending input — a recall returns the text
// so the caller can restore it instead of losing the line. An entry
// carrying a receipt is marked recalled, so the exact receipt the web
// submitted reflects the CLI withdrawal too.
func (s *SteeringInput) popLast() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return "", false
	}
	last := s.pending[len(s.pending)-1]
	s.pending = s.pending[:len(s.pending)-1]
	if last.receipt != nil {
		last.receipt.markRecalled()
	}
	return last.input.Text, true
}

// claim detaches every pending entry oldest-first, flipping each receipt
// to claimed in the same critical section — a recall that loses the
// race finds a non-pending state, never a second delivery.
func (s *SteeringInput) claim() []steeringEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	out := s.pending
	s.pending = nil
	for _, e := range out {
		if e.receipt != nil {
			e.receipt.markClaimed()
		}
	}
	return out
}
