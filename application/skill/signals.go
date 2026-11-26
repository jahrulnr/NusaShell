package skill

import "strings"

// longRounds is the count of model rounds with tool calls at which a turn is long.
const longRounds = 8

// Signals are the structural facts that make a finished turn worth a log line.
type Signals struct {
	OwnerRewrote bool // the owner rewrote the message that opened the turn
	Recovered    bool // a model call failed, and a later call of the same tool succeeded
	Long         bool // the turn made at least longRounds rounds of model calls
	// Unrecovered is the complement of Recovered: some model call failed and
	// no later call of the same tool succeeded. It is a fact about the turn,
	// not a signal of interest, so it stays out of Any and Names.
	Unrecovered bool
	// DeadEnd sharpens Unrecovered: the failure never recovered AND the owner
	// never spoke after the last failed call. An owner line after the failure
	// means the turn was steered — the failure is part of the story the owner
	// corrected, not the end of it — so the turn still carries the strongest
	// evidence this feature has. Silence means the turn walked into a dead
	// end: no working procedure, no owner judgment, nothing to teach.
	DeadEnd bool
}

// Detect reads the signals of one window. rewrote says whether the room's
// recorded owner rewrite targeted the window's anchor.
func Detect(w Window, rewrote bool) Signals {
	s := Signals{OwnerRewrote: rewrote}
	failed := map[string]bool{}
	open := map[string]bool{}
	failUnanswered := false
	rounds := 0
	for _, m := range w.Msgs {
		if m.Editable() {
			// An owner line answers every failure before it; what matters is
			// the last one, so steering after the first failure does not
			// rescue a turn that fails again into silence.
			failUnanswered = false
		}
		round := false
		for _, tc := range m.ToolCalls() {
			if !modelCall(tc) {
				continue
			}
			round = true
			switch {
			case tc.Failed():
				failed[tc.Name()] = true
				open[tc.Name()] = true
				failUnanswered = true
			case tc.HasOutput():
				if failed[tc.Name()] {
					s.Recovered = true
				}
				delete(open, tc.Name())
			}
		}
		if round {
			rounds++
		}
	}
	s.Long = rounds >= longRounds
	s.Unrecovered = len(open) > 0
	s.DeadEnd = s.Unrecovered && failUnanswered
	return s
}

// Any reports whether at least one signal fired.
func (s Signals) Any() bool { return s.OwnerRewrote || s.Recovered || s.Long }

// Names lists the fired signals, sorted and comma-joined: the log's signals field.
func (s Signals) Names() string {
	var names []string
	if s.Long {
		names = append(names, "long")
	}
	if s.OwnerRewrote {
		names = append(names, "owner_rewrote")
	}
	if s.Recovered {
		names = append(names, "recovered")
	}
	return strings.Join(names, ",")
}
