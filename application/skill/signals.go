package skill

import "strings"

// longRounds is the count of model rounds with tool calls at which a turn is long.
const longRounds = 8

// Signals are the structural facts that make a finished turn worth a log line.
type Signals struct {
	OwnerRewrote bool // the owner rewrote the message that opened the turn
	Recovered    bool // a model call failed, and a later call of the same tool succeeded
	Long         bool // the turn made at least longRounds rounds of model calls
}

// Detect reads the signals of one window. rewrote says whether the room's
// recorded owner rewrite targeted the window's anchor.
func Detect(w Window, rewrote bool) Signals {
	s := Signals{OwnerRewrote: rewrote}
	failed := map[string]bool{}
	rounds := 0
	for _, m := range w.Msgs {
		round := false
		for _, tc := range m.ToolCalls() {
			if !modelCall(tc) {
				continue
			}
			round = true
			switch {
			case tc.Failed():
				failed[tc.Name()] = true
			case tc.HasOutput() && failed[tc.Name()]:
				s.Recovered = true
			}
		}
		if round {
			rounds++
		}
	}
	s.Long = rounds >= longRounds
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
