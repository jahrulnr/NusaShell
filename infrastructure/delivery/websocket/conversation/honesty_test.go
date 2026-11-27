package wsconversation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

func TestAgentTurnOpensPreparingAndFailsWithItsClass(t *testing.T) {
	var n int
	h := newHub(t, stubSender{fn: func(_ context.Context, convID, _, _, _ string, onEvent event.TurnEventHandler) (appconversation.SendResult, error) {
		n++
		if n > 1 {
			return appconversation.SendResult{ConversationID: convID, UserMessagePersisted: true}, nil
		}
		onEvent(event.NewClockEvent("working", 0, 99))
		const prompted int64 = 1_700_000_000_000
		onEvent(event.NewClockEvent("waiting", prompted, 0))
		onEvent(event.NewClockEvent("working", prompted, prompted+5))
		return appconversation.SendResult{ConversationID: convID, UserMessagePersisted: true},
			apperror.NewAgentFailure(apperror.AgentClassCredential, 403, "agent_credential", "invalid api key", nil, 0)
	}})
	ws := dial(t, h)
	sendCommand(t, ws, `{"type":"turn.send","requestId":"s1","payload":{"content":"x","agentId":"fake"}}`)
	var opened, failed frame
	var phases []phasePayload
	for {
		f := readFrame(t, ws)
		switch f.Type {
		case "turn.opened":
			opened = f
		case "turn.phase":
			var p phasePayload
			if err := json.Unmarshal(f.Payload, &p); err != nil {
				t.Fatal(err)
			}
			if p.Phase == "clock" {
				phases = append(phases, p)
			}
		case "turn.failed":
			failed = f
		case "error":
			t.Fatalf("frame = %+v", f)
		}
		if failed.Type == "turn.failed" {
			break
		}
	}
	var open openedPayload
	if err := json.Unmarshal(opened.Payload, &open); err != nil {
		t.Fatal(err)
	}
	if open.Clock != "preparing" {
		t.Fatalf("opened clock = %q, want preparing", open.Clock)
	}
	sawWorking := false
	for _, p := range phases {
		if p.Clock == "working" && p.PromptedAt == nil {
			t.Fatalf("working while promptedAt is absent: %+v", p)
		}
		if p.Clock == "working" && p.PromptedAt != nil {
			sawWorking = true
		}
	}
	if !sawWorking {
		t.Fatalf("phases = %+v, want a working clock after the prompt", phases)
	}
	var term terminalPayload
	if err := json.Unmarshal(failed.Payload, &term); err != nil {
		t.Fatal(err)
	}
	if term.Status != 403 || term.Code != "agent_credential" || term.AgentClass != "credential" || term.Retry != "none" {
		t.Fatalf("terminal = %+v", term)
	}
	if term.RetryAt != nil {
		t.Fatalf("retryAt = %d, want it omitted when no delay was named", *term.RetryAt)
	}
	if term.AgentMessage != "invalid api key" || strings.Contains(term.Message, "agent turn failed") {
		t.Fatalf("message = %q agentMessage = %q", term.Message, term.AgentMessage)
	}
	if term.UserMessagePersisted == nil || !*term.UserMessagePersisted {
		t.Fatal("send failure dropped userMessagePersisted")
	}
	if term.PromptedAt == nil || *term.PromptedAt == 0 || term.LastUpdateAt == nil || *term.LastUpdateAt == 0 {
		t.Fatalf("stamps prompted %v updated %v", term.PromptedAt, term.LastUpdateAt)
	}
	if open.StartedAt == *term.PromptedAt {
		t.Fatal("promptedAt was copied from startedAt")
	}

	sendCommand(t, ws, `{"type":"turn.send","requestId":"s2","payload":{"content":"y"}}`)
	second := readFrame(t, ws)
	for second.Type != "turn.opened" {
		second = readFrame(t, ws)
	}
	if strings.Contains(string(second.Payload), "clock") {
		t.Fatalf("provider turn.opened carried a clock: %s", second.Payload)
	}
}

func TestStampsAreOmittedWhenAbsent(t *testing.T) {
	raw, err := json.Marshal(terminalPayload{ResyncRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"promptedAt", "lastUpdateAt", "agentClass", "retryAt", "null"} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("absent terminal wrote %s: %s", key, raw)
		}
	}
	opened, err := json.Marshal(openedPayload{StartedAt: 1, Steerable: false})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(opened), "clock") {
		t.Fatalf("provider opened wrote a clock: %s", opened)
	}
	at := int64(5)
	stamped, err := json.Marshal(terminalPayload{ResyncRequired: true, PromptedAt: &at})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stamped), `"promptedAt":5`) || strings.Contains(string(stamped), "lastUpdateAt") {
		t.Fatalf("stamped terminal = %s", stamped)
	}
}

func TestActiveSnapshotCarriesAgentStamps(t *testing.T) {
	h := NewHub(t.Context(), stubSender{fn: func(context.Context, string, string, string, string, event.TurnEventHandler) (appconversation.SendResult, error) {
		return appconversation.SendResult{}, nil
	}})
	t.Cleanup(h.Shutdown)
	active := &turn{
		id: "t_snap", conversationID: "c_snap", kind: turnSend,
		startedAt: time.UnixMilli(1), live: newLiveDoc(),
		agent: true, clock: "waiting", promptedAt: 10, lastUpdateAt: 25,
		subscribed: map[*conn]struct{}{},
	}
	h.mu.Lock()
	body := h.resyncForLocked(active)
	h.mu.Unlock()
	if body.Turn != "active" || body.Clock != "waiting" || body.PromptedAt == nil || *body.PromptedAt != 10 || body.LastUpdateAt == nil || *body.LastUpdateAt != 25 {
		t.Fatalf("snapshot = %+v", body)
	}
	if body.StartedAt == *body.PromptedAt {
		t.Fatal("promptedAt was copied from startedAt")
	}
	bare := &turn{
		id: "t_bare", conversationID: "c_bare", kind: turnSend,
		startedAt: time.UnixMilli(1), live: newLiveDoc(),
		subscribed: map[*conn]struct{}{},
	}
	h.mu.Lock()
	empty := h.resyncForLocked(bare)
	h.mu.Unlock()
	raw, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"promptedAt", "lastUpdateAt", "clock"} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("provider snapshot wrote %s: %s", key, raw)
		}
	}
	h.onEvent(bare, event.NewClockEvent("working", 0, 99))
	if bare.clock == "working" || bare.lastUpdateAt != 0 {
		t.Fatalf("working before the prompt stuck: clock %q updated %d", bare.clock, bare.lastUpdateAt)
	}
}
