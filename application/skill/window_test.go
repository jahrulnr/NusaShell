package skill

import (
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// newMsg is a message of the stored shape, with a fixed id.
func newMsg(t *testing.T, id string, role enum.Role, text string) entity.Message {
	t.Helper()
	m, err := entity.NewMessage(role, text)
	if err != nil {
		t.Fatal(err)
	}
	m.SetID(id)
	return m
}

// finalReply is an assistant reply that finished normally.
func finalReply(t *testing.T, id, text string) entity.Message {
	t.Helper()
	m := newMsg(t, id, enum.RoleAssistant, text)
	m.SetFinishReason(enum.FinishReasonStop)
	return m
}

// toolRound is an assistant reply that made one tool call of the given type.
func toolRound(t *testing.T, id, name string, typ enum.Tooltype, output string, failed bool) entity.Message {
	t.Helper()
	m := newMsg(t, id, enum.RoleAssistant, "")
	callID := id + "-call"
	tc, err := entity.NewToolCall(callID, name, "{}", typ)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AddToolCall(tc); err != nil {
		t.Fatal(err)
	}
	if output != "" {
		if err := m.SetToolOutput(callID, output); err != nil {
			t.Fatal(err)
		}
	}
	if failed {
		if err := m.SetToolFailed(callID, true); err != nil {
			t.Fatal(err)
		}
	}
	m.SetFinishReason(enum.FinishReasonToolCalls)
	return m
}

// callRound is a reply that made one model tool call.
func callRound(t *testing.T, id, name, output string, failed bool) entity.Message {
	t.Helper()
	return toolRound(t, id, name, enum.TooltypeOrigin, output, failed)
}

// originRecord is the system record appendOriginContext writes for a turn.
func originRecord(t *testing.T, id, kind string) entity.Message {
	t.Helper()
	m := newMsg(t, id, enum.RoleSystem, "")
	tc, err := entity.NewToolCall(id+"-call", "origin", `{"kind":"`+kind+`"}`, enum.TooltypeInjection)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AddToolCall(tc); err != nil {
		t.Fatal(err)
	}
	return m
}

// room is a live tail holding the given messages in order.
func room(t *testing.T, msgs ...entity.Message) *aggregate.Conversation {
	t.Helper()
	c, err := aggregate.NewConversation("room")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		c.Append(m)
	}
	return c
}

func TestTurnWindowAnchorsOnTheNewestOwnerLine(t *testing.T) {
	conv := room(t,
		newMsg(t, "m1", enum.RoleUser, "first"),
		finalReply(t, "a1", "answer"),
		newMsg(t, "m2", enum.RoleUser, "second"),
		finalReply(t, "a2", "final"),
	)
	w, ok := TurnWindow(conv.Messages())
	if !ok || w.AnchorID != "m2" || w.Owner != "second" || len(w.Msgs) != 1 || w.Msgs[0].ID() != "a2" {
		t.Fatalf("window = anchor %q owner %q msgs %d, ok %v", w.AnchorID, w.Owner, len(w.Msgs), ok)
	}
}

// A steer line is an owner line, so it joins the window, but it never becomes
// the anchor: the turn still opened at the message before it.
func TestTurnWindowSteerLineJoinsButDoesNotMoveTheAnchor(t *testing.T) {
	steer := newMsg(t, "s1", enum.RoleUser, "also check the lock")
	steer.SetInputID("st-1")
	conv := room(t,
		newMsg(t, "m1", enum.RoleUser, "fix it"),
		callRound(t, "a1", "read", "ok", false),
		steer,
		finalReply(t, "a2", "done"),
	)
	w, ok := TurnWindow(conv.Messages())
	if !ok || w.AnchorID != "m1" || w.Owner != "also check the lock" || len(w.Msgs) != 3 {
		t.Fatalf("window = anchor %q owner %q msgs %d, ok %v", w.AnchorID, w.Owner, len(w.Msgs), ok)
	}
}

func TestTurnWindowLeavesSystemRecordsOut(t *testing.T) {
	conv := room(t,
		newMsg(t, "m1", enum.RoleUser, "fix it"),
		originRecord(t, "o1", "web"),
		finalReply(t, "a1", "done"),
	)
	w, ok := TurnWindow(conv.Messages())
	if !ok || len(w.Msgs) != 1 || w.Msgs[0].ID() != "a1" {
		t.Fatalf("msgs = %d, ok %v; want only the reply", len(w.Msgs), ok)
	}
}

// Another thread's prompt is data in this room, never the owner's words.
func TestTurnWindowRelayLineIsOtherTextNotAnOwnerLine(t *testing.T) {
	relay := newMsg(t, "r1", enum.RoleUser, "from another thread")
	relay.SetUserType(enum.LLMUserTypeRelay)
	conv := room(t,
		newMsg(t, "m1", enum.RoleUser, "mine"),
		relay,
		finalReply(t, "a1", "done"),
	)
	w, ok := TurnWindow(conv.Messages())
	if !ok || w.Owner != "mine" || !strings.Contains(w.Digest, "other: from another thread\n") || strings.Contains(w.Digest, "owner: from another thread") {
		t.Fatalf("owner %q, digest %q, ok %v", w.Owner, w.Digest, ok)
	}
}

func TestTurnWindowWithoutAnOwnerLineIsNotOK(t *testing.T) {
	relay := newMsg(t, "r1", enum.RoleUser, "from another thread")
	relay.SetUserType(enum.LLMUserTypeRelay)
	if _, ok := TurnWindow(room(t, relay, finalReply(t, "a1", "done")).Messages()); ok {
		t.Fatal("a tail with no owner line has a window")
	}
}

func TestTurnWindowKeepsOwnerLinesAndClipsModelText(t *testing.T) {
	owner := strings.Repeat("o", 700)
	steer := newMsg(t, "s1", enum.RoleUser, owner)
	steer.SetInputID("st-1")
	conv := room(t,
		newMsg(t, "m1", enum.RoleUser, "start"),
		callRound(t, "a1", "read", strings.Repeat("e", 700), true),
		finalReply(t, "a2", strings.Repeat("é", 700)),
		steer,
	)
	w, ok := TurnWindow(conv.Messages())
	if !ok {
		t.Fatal("no window")
	}
	if w.Owner != owner {
		t.Fatal("the newest owner line of the window is not the steer line")
	}
	for _, want := range []string{
		"owner: " + owner + "\n",
		"tool read failed=true\n",
		"error: " + strings.Repeat("e", 600) + "\n",
		"assistant: " + strings.Repeat("é", 600) + "\n",
	} {
		if !strings.Contains(w.Digest, want) {
			t.Fatalf("digest lacks the line %q", want)
		}
	}
	if strings.Contains(w.Digest, strings.Repeat("e", 601)) || strings.Contains(w.Digest, strings.Repeat("é", 601)) {
		t.Fatal("model text was not clipped to 600 runes")
	}
}
