package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

func TestServerCompactionContextManagementEligibleModel(t *testing.T) {
	cases := []struct {
		model         string
		wantThreshold int
	}{
		{"gpt-5.2", 360000},      // 400k * 0.9
		{"gpt-5.6-luna", 942818}, // 1047576 * 0.9
		{"gpt-4.1", 942818},      // 1047576 * 0.9
		{"o3", 180000},           // 200k * 0.9
		{"o1", 180000},           // 200k * 0.9
	}
	for _, tc := range cases {
		cm := serverCompactionContextManagement(tc.model)
		if cm == nil {
			t.Fatalf("serverCompactionContextManagement(%q) = nil, want non-nil", tc.model)
		}
		if len(cm) != 1 {
			t.Fatalf("cm len = %d, want 1", len(cm))
		}
		if cm[0]["type"] != "compaction" {
			t.Fatalf("cm[0].type = %v, want compaction", cm[0]["type"])
		}
		threshold, ok := cm[0]["compact_threshold"].(int)
		if !ok {
			t.Fatalf("cm[0].compact_threshold = %T, want int", cm[0]["compact_threshold"])
		}
		if threshold != tc.wantThreshold {
			t.Errorf("serverCompactionContextManagement(%q) threshold = %d, want %d", tc.model, threshold, tc.wantThreshold)
		}
	}
}

func TestServerCompactionContextManagementIneligibleModel(t *testing.T) {
	cases := []string{
		"gpt-4o",      // 128k context, below 200k floor
		"gpt-4-turbo", // not in table
		"claude-sonnet-4",
		"",
	}
	for _, model := range cases {
		cm := serverCompactionContextManagement(model)
		if cm != nil {
			t.Errorf("serverCompactionContextManagement(%q) = %v, want nil", model, cm)
		}
	}
}

func TestServerCompactionContextManagementFloorEnforced(t *testing.T) {
	// Temporarily raise the floor to verify it clamps. The threshold is
	// computed in the domain, so the floor must be mutated there.
	original := domain.ServerCompactionThresholdFloor
	domain.ServerCompactionThresholdFloor = 200000
	defer func() { domain.ServerCompactionThresholdFloor = original }()

	// o3 has 200k context, 0.9 * 200k = 180k < 200k floor → should use floor
	cm := serverCompactionContextManagement("o3")
	if cm == nil {
		t.Fatalf("serverCompactionContextManagement(o3) = nil, want non-nil")
	}
	threshold, _ := cm[0]["compact_threshold"].(int)
	if threshold != 200000 {
		t.Errorf("threshold = %d, want 200000 (floor)", threshold)
	}
}

func TestFromCoreResponseCarriesCompactionItems(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{"type":"compaction","encrypted_content":"ENC-1"}`),
		json.RawMessage(`{"type":"compaction","encrypted_content":"ENC-2"}`),
	}
	resp := &core.Response{
		Blocks:          []core.Block{core.Text("answer")},
		CompactionItems: items,
	}
	out := FromCoreResponse(resp)
	if len(out.CompactionItems) != 2 {
		t.Fatalf("CompactionItems len = %d, want 2", len(out.CompactionItems))
	}
	if string(out.CompactionItems[0]) != `{"type":"compaction","encrypted_content":"ENC-1"}` {
		t.Fatalf("CompactionItems[0] = %s", out.CompactionItems[0])
	}
}

func TestCompactConversationSkipsForServerSideEligibleModel(t *testing.T) {
	// For server-side eligible models, compactConversation should return
	// immediately without doing any client-side summarization.
	keepFiller := strings.Repeat("keep-me-recent-user-message-", 40)
	msgs := []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "question-one", Status: domain.StatusDone},
		{ID: "a1", Role: domain.RoleAssistant, Content: "answer-one", Status: domain.StatusDone},
	}
	for i := 0; i < 12; i++ {
		msgs = append(msgs, domain.Message{
			ID: fmt.Sprintf("keep%d", i), Role: domain.RoleUser, Content: keepFiller, Status: domain.StatusDone,
		})
	}
	conv := &domain.Conversation{ID: "c-server-skip", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c-server-skip": conv}}
	adapter := &recordingCompleteAdapter{toolCallSummaries: []string{validTestSummary}}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	settings := domain.DefaultSettings()

	summary, err := app.compactConversation(context.Background(), stubProviderContext(adapter), conv, "gpt-5.2", 4000, settings, domain.CompactionTriggerInitial)
	if err != nil {
		t.Fatalf("compactConversation: %v", err)
	}
	if summary != "" {
		t.Fatalf("summary = %q, want empty for server-side eligible model", summary)
	}
	// The recording adapter should NOT have been called.
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.requests) != 0 {
		t.Fatalf("adapter requests = %d, want 0 (server-side eligible skips client-side)", len(adapter.requests))
	}
}
