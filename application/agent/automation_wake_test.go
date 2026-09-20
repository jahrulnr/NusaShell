package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"nusashell/domain"
)

func TestDeliverAutomationWakeWakesIdleConversation(t *testing.T) {
	conv := &domain.Conversation{
		ID: "conv_target", Model: "model", Status: "idle",
		Messages: []domain.Message{{ID: "u1", Role: domain.RoleUser, Content: "start", Status: domain.StatusDone}},
	}
	service, store, launched := peerWakeService(t, conv)

	if err := service.DeliverAutomationWake("conv_target", "wf-minecraft", "Steve: hello"); err != nil {
		t.Fatalf("DeliverAutomationWake: %v", err)
	}

	saved, err := store.Get("conv_target")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "running" {
		t.Fatalf("status = %q, want running", saved.Status)
	}
	if len(saved.PendingAnnouncements) != 0 {
		t.Fatalf("pending announcements = %+v, want drained before wake", saved.PendingAnnouncements)
	}
	if len(saved.Messages) != 3 {
		t.Fatalf("messages = %d, want user + announcement + assistant placeholder", len(saved.Messages))
	}
	ann := saved.Messages[1].ToolCalls[0]
	if ann.Name != domain.AnnouncementToolName || !strings.Contains(ann.Args, "automation_event") {
		t.Fatalf("announcement tool call = %+v", ann)
	}
	if !strings.Contains(ann.Output, "Steve: hello") {
		t.Fatalf("announcement output must carry the message: %q", ann.Output)
	}
	if !launched() {
		t.Fatal("idle automation wake must launch an agent turn")
	}
}

func TestDeliverAutomationWakeQueuesWhileConversationIsActive(t *testing.T) {
	conv := &domain.Conversation{
		ID: "conv_target", Model: "model", Status: "running",
		Messages: []domain.Message{{ID: "u1", Role: domain.RoleUser, Content: "start", Status: domain.StatusDone}},
	}
	service, store, launched := peerWakeService(t, conv)
	service.runsMu.Lock()
	service.runs["run-existing"] = &TurnRun{ID: "run-existing", ConversationID: "conv_target"}
	service.runsMu.Unlock()

	if err := service.DeliverAutomationWake("conv_target", "wf-minecraft", "queued fact"); err != nil {
		t.Fatalf("DeliverAutomationWake: %v", err)
	}

	saved, err := store.Get("conv_target")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.PendingAnnouncements) != 1 || saved.PendingAnnouncements[0].Type != "automation_event" {
		t.Fatalf("pending announcements = %+v, want one automation_event", saved.PendingAnnouncements)
	}
	if launched() {
		t.Fatal("busy room must not launch a second run")
	}
}

func TestDeliverAutomationWakeValidatesInput(t *testing.T) {
	conv := &domain.Conversation{
		ID: "conv_target", Model: "model", Status: "idle",
		Messages: []domain.Message{{ID: "u1", Role: domain.RoleUser, Content: "start", Status: domain.StatusDone}},
	}
	service, _, _ := peerWakeService(t, conv)
	if err := service.DeliverAutomationWake("", "wf", "msg"); err == nil {
		t.Fatal("empty conversation id must error")
	}
	if err := service.DeliverAutomationWake("conv_target", "wf", "  "); err == nil {
		t.Fatal("empty message must error")
	}
}

func TestConversationWakeCapabilityExecutes(t *testing.T) {
	var gotTarget, gotSource, gotMessage string
	cap := ConversationWakeCapability(func(targetID, source, content string) error {
		gotTarget, gotSource, gotMessage = targetID, source, content
		return nil
	})
	out, err := cap.Execute(context.Background(), json.RawMessage(`{"conversation":"conv_1","source":"wf-minecraft","message":"hi"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotTarget != "conv_1" || gotSource != "wf-minecraft" || gotMessage != "hi" {
		t.Fatalf("wake got (%q,%q,%q)", gotTarget, gotSource, gotMessage)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil || parsed["ok"] != true {
		t.Fatalf("output must be JSON {ok:true,...}: %s", out)
	}
}

func TestConversationWakeCapabilityRejectsBadInput(t *testing.T) {
	called := false
	cap := ConversationWakeCapability(func(_, _, _ string) error { called = true; return nil })
	if _, err := cap.Execute(context.Background(), json.RawMessage(`{"message":"no target"}`)); err == nil {
		t.Fatal("missing conversation must error")
	}
	if _, err := cap.Execute(context.Background(), json.RawMessage(`not json`)); err == nil {
		t.Fatal("malformed input must error")
	}
	if called {
		t.Fatal("invalid input must not reach the wake func")
	}
}
