// Package skill holds skill improvement. The observer reads the store and logs
// one line for each eligible turn that shows a signal; the validator admits or
// refuses learned-skill drafts against owner evidence, the live catalog, and the
// content scan.
package skill

import (
	"context"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

const (
	// observeTimeout bounds one observation's read of the room.
	observeTimeout = 30 * time.Second
	// redactClip bounds the owner text redaction sees. Redaction runs before
	// observeClip, so a key that straddles observeClip is masked whole.
	redactClip = 4096
	// observeClip bounds the owner text the log line carries.
	observeClip = 120
)

// Observer logs one skill_observe line per eligible finished turn that shows a
// signal, and nothing for a turn without one. It reads the store and writes only
// the log. A failed observation is logged and skipped; it never affects the turn.
type Observer struct {
	convs  drepo.Conversation
	ev     *Evidence
	redact func(string) string
	log    *slog.Logger
}

// NewObserver wires the observer. The composition root registers its Observe
// only when skills.improvement is observe, so an off build never reaches it.
func NewObserver(convs drepo.Conversation, ev *Evidence, redact func(string) string, log *slog.Logger) *Observer {
	return &Observer{convs: convs, ev: ev, redact: redact, log: log}
}

// Observe is the hub's finished hook for one turn: kind is the command that
// opened it (send, edit, or resume), origin is where it started, and err the
// turn's error.
func (o *Observer) Observe(convID, kind string, origin valueobject.TurnOrigin, err error) {
	if err != nil {
		return
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), observeTimeout)
	defer cancel()
	conv, loadErr := o.convs.Load(ctx, convID)
	if loadErr != nil {
		o.log.Warn("skill_observe_failed", "conv", convID, "err", o.redact(loadErr.Error()))
		return
	}
	w, ok := TurnWindow(conv.Messages())
	if !ok {
		return
	}
	rewrote := false
	if kind == "edit" {
		_, rewrote = o.ev.Take(convID, w.AnchorID)
	}
	if !eligible(w, origin) {
		return
	}
	s := Detect(w, rewrote)
	if !s.Any() {
		return
	}
	owner := clip(o.redact(clip(w.Owner, redactClip)), observeClip)
	o.log.Info("skill_observe", "conv", convID, "kind", kind, "signals", s.Names(),
		"window_msgs", len(w.Msgs), "window_runes", utf8.RuneCountInString(w.Digest),
		"owner", owner, "elapsed_ms", time.Since(start).Milliseconds())
}

// TurnEligible is the gate the composition root shares with the observer: a
// finished reply that was not interrupted, from a person present. The origin
// comes from the hub: an agent room writes no origin record of its own.
func TurnEligible(w Window, origin valueobject.TurnOrigin) bool { return eligible(w, origin) }

// eligible is the gate behind TurnEligible. The window must end on a finished
// reply that was not interrupted, and the origin must name a person present.
func eligible(w Window, origin valueobject.TurnOrigin) bool {
	if len(w.Msgs) == 0 {
		return false
	}
	last := w.Msgs[len(w.Msgs)-1]
	if last.Role() != enum.RoleAssistant || last.FinishReason() == "" || last.FinishReason() == enum.FinishReasonInterrupted {
		return false
	}
	return origin.Kind == enum.TurnOriginWeb || origin.Kind == enum.TurnOriginChannel
}
