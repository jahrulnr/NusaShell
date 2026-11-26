package skill

import (
	"fmt"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// windowOf is the window of a turn that opened at owner line m1 and ran msgs.
func windowOf(t *testing.T, msgs ...entity.Message) Window {
	t.Helper()
	w, ok := TurnWindow(room(t, append([]entity.Message{newMsg(t, "m1", enum.RoleUser, "start")}, msgs...)...).Messages())
	if !ok {
		t.Fatal("no window")
	}
	return w
}

func TestSignalRecoveredNeedsAFailureThenASuccessOfTheSameTool(t *testing.T) {
	for _, tc := range []struct {
		name string
		msgs []entity.Message
		want bool
	}{
		{"failure then success of the same tool", []entity.Message{callRound(t, "a1", "search", "", true), callRound(t, "a2", "search", "hits", false)}, true},
		{"success then failure", []entity.Message{callRound(t, "a1", "search", "hits", false), callRound(t, "a2", "search", "", true)}, false},
		{"failure then success of another tool", []entity.Message{callRound(t, "a1", "search", "", true), callRound(t, "a2", "read", "text", false)}, false},
		{"an injection failure does not count", []entity.Message{toolRound(t, "a1", "search", enum.TooltypeInjection, "", true), callRound(t, "a2", "search", "hits", false)}, false},
	} {
		if got := Detect(windowOf(t, tc.msgs...), false).Recovered; got != tc.want {
			t.Fatalf("%s: Recovered = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSignalLongCountsModelRoundsNotInjections(t *testing.T) {
	rounds := func(n int, typ enum.Tooltype) []entity.Message {
		var msgs []entity.Message
		for i := range n {
			msgs = append(msgs, toolRound(t, fmt.Sprintf("a%d", i), "read", typ, "ok", false))
		}
		return msgs
	}
	if Detect(windowOf(t, rounds(7, enum.TooltypeOrigin)...), false).Long {
		t.Fatal("seven model rounds are not long")
	}
	if !Detect(windowOf(t, rounds(8, enum.TooltypeOrigin)...), false).Long {
		t.Fatal("eight model rounds are long")
	}
	if Detect(Window{Msgs: rounds(8, enum.TooltypeInjection)}, false).Long {
		t.Fatal("injection calls were counted as model rounds")
	}
}

func TestSignalOwnerRewroteFollowsTheRecordedRewrite(t *testing.T) {
	w := windowOf(t, finalReply(t, "a1", "done"))
	if !Detect(w, true).OwnerRewrote || Detect(w, false).OwnerRewrote {
		t.Fatal("OwnerRewrote does not follow the rewrite that targeted the anchor")
	}
}

func TestSignalUnrecoveredIsTheComplementOfRecovered(t *testing.T) {
	for _, tc := range []struct {
		name string
		msgs []entity.Message
		want bool
	}{
		{"a failure alone is unrecovered", []entity.Message{callRound(t, "a1", "search", "", true)}, true},
		{"failure then success of the same tool recovers", []entity.Message{callRound(t, "a1", "search", "", true), callRound(t, "a2", "search", "hits", false)}, false},
		{"success of another tool does not recover this one", []entity.Message{callRound(t, "a1", "search", "", true), callRound(t, "a2", "read", "text", false)}, true},
		{"a later failure reopens what mid-turn success closed", []entity.Message{callRound(t, "a1", "search", "", true), callRound(t, "a2", "search", "hits", false), callRound(t, "a3", "search", "", true)}, true},
		{"a clean turn has nothing to recover", []entity.Message{callRound(t, "a1", "search", "hits", false)}, false},
		{"an injection failure does not count", []entity.Message{toolRound(t, "a1", "search", enum.TooltypeInjection, "", true)}, false},
	} {
		if got := Detect(windowOf(t, tc.msgs...), false).Unrecovered; got != tc.want {
			t.Fatalf("%s: Unrecovered = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSignalDeadEndNeedsSilenceAfterTheLastFailure(t *testing.T) {
	steer := func(id, text string) entity.Message {
		m := newMsg(t, id, enum.RoleUser, text)
		m.SetInputID("st-" + id)
		return m
	}
	for _, tc := range []struct {
		name string
		msgs []entity.Message
		want bool
	}{
		{"failure into silence is a dead end", []entity.Message{callRound(t, "a1", "search", "", true)}, true},
		{"an owner line after the failure means the turn was steered", []entity.Message{callRound(t, "a1", "search", "", true), steer("s1", "not like that — restore the snapshot first")}, false},
		{"owner speech before the last failure does not rescue it", []entity.Message{callRound(t, "a1", "search", "", true), steer("s1", "try again"), callRound(t, "a2", "search", "", true)}, true},
		{"a recovered failure is never a dead end", []entity.Message{callRound(t, "a1", "search", "", true), callRound(t, "a2", "search", "hits", false)}, false},
		{"steering, then a different unrecovered failure into silence", []entity.Message{callRound(t, "a1", "search", "", true), steer("s1", "use read instead"), callRound(t, "a2", "read", "", true)}, true},
		{"a clean turn has no dead end", []entity.Message{callRound(t, "a1", "search", "hits", false)}, false},
	} {
		if got := Detect(windowOf(t, tc.msgs...), false).DeadEnd; got != tc.want {
			t.Fatalf("%s: DeadEnd = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSignalUnrecoveredStaysOutOfAnyAndNames(t *testing.T) {
	// Unrecovered is a negative fact, not a signal of interest: it must never
	// make a quiet turn look worth a pass.
	s := Signals{Unrecovered: true, DeadEnd: true}
	if s.Any() || s.Names() != "" {
		t.Fatal("a dead-end turn named a positive signal")
	}
}

func TestSignalNamesAreSortedAndEmptyWhenQuiet(t *testing.T) {
	all := Signals{OwnerRewrote: true, Recovered: true, Long: true}
	if got := all.Names(); got != "long,owner_rewrote,recovered" {
		t.Fatalf("Names = %q", got)
	}
	if quiet := (Signals{}); quiet.Any() || quiet.Names() != "" {
		t.Fatal("a quiet turn names a signal")
	}
}
