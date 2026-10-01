package jsonstore

import (
	"testing"
	"time"

	"nusashell/domain"
)

// TestListMetaProjectsMetadataWithoutTranscript pins the contract of the
// metadata projection: every scalar field survives, the message list keeps
// its length and roles (ConvDTO's MessageCount and HasDurableAnchor depend on
// them), no message content leaks, and mutating a projection cannot write
// into the store.
func TestListMetaProjectsMetadataWithoutTranscript(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	old := &domain.Conversation{
		ID:              "conv_meta_old",
		Title:           "older",
		Model:           "prov_x:model-y",
		Effort:          "high",
		ProviderRoute:   "route-a",
		Status:          "idle",
		Workspace:       "/tmp/ws",
		ChunkCount:      2,
		EstimatedTokens: 1234,
		ContextTokens:   999,
		Type:            domain.ConversationTypeConversation,
		CreatedAt:       time.Now().UTC().Add(-2 * time.Hour),
		UpdatedAt:       time.Now().UTC().Add(-time.Hour),
		PendingAnnouncements: []domain.PendingAnnouncement{
			{ID: "pa_1", Type: "peer_message", Message: "ping"},
		},
		LastAnnouncedRecords: domain.AnnouncedRecords{{ID: "mem_1"}},
		Messages: []domain.Message{
			{ID: "msg_1", Role: domain.RoleUser, Content: "secret body"},
			{ID: "msg_2", Role: domain.RoleAssistant, Content: "secret reply"},
		},
	}
	newer := newTestConversation("conv_meta_new",
		domain.Message{ID: "msg_1", Role: domain.RoleUser, Content: "hi"},
	)
	newer.UpdatedAt = time.Now().UTC()
	hidden := newTestConversation("conv_meta_job")
	hidden.Type = domain.ConversationTypeBackground
	hidden.UpdatedAt = time.Now().UTC().Add(-30 * time.Minute)
	for _, c := range []*domain.Conversation{old, newer, hidden} {
		if err := store.Save(c); err != nil {
			t.Fatalf("Save %s: %v", c.ID, err)
		}
	}

	metas := store.ListMeta()
	if len(metas) != 3 {
		t.Fatalf("ListMeta len = %d, want 3", len(metas))
	}
	// Same ordering as List: UpdatedAt descending.
	if metas[0].ID != "conv_meta_new" || metas[1].ID != "conv_meta_job" || metas[2].ID != "conv_meta_old" {
		t.Fatalf("ListMeta order mismatch: %s, %s, %s", metas[0].ID, metas[1].ID, metas[2].ID)
	}

	var meta *domain.Conversation
	for _, m := range metas {
		if m.ID == "conv_meta_old" {
			meta = m
		}
		if m.ID == "conv_meta_job" && !m.HiddenFromRoomList() {
			t.Fatal("HiddenFromRoomList must work on a meta projection")
		}
	}
	if meta == nil {
		t.Fatal("conv_meta_old missing from ListMeta")
	}
	if meta.Title != "older" || meta.Model != "prov_x:model-y" || meta.Effort != "high" ||
		meta.ProviderRoute != "route-a" || meta.Workspace != "/tmp/ws" || meta.ChunkCount != 2 ||
		meta.EstimatedTokens != 1234 || meta.ContextTokens != 999 {
		t.Fatalf("meta projection lost scalar fields: %+v", meta)
	}
	if len(meta.PendingAnnouncements) != 1 || meta.PendingAnnouncements[0].Message != "ping" {
		t.Fatalf("PendingAnnouncements must be preserved: %+v", meta.PendingAnnouncements)
	}
	if len(meta.LastAnnouncedRecords) != 1 || meta.LastAnnouncedRecords[0].ID != "mem_1" {
		t.Fatalf("LastAnnouncedRecords must be preserved: %+v", meta.LastAnnouncedRecords)
	}
	// Message count and roles survive (that is what MessageCount and
	// HasUserMessage/HasDurableAnchor read), content does not.
	if len(meta.Messages) != 2 {
		t.Fatalf("meta message count = %d, want 2", len(meta.Messages))
	}
	if meta.Messages[0].Role != domain.RoleUser || meta.Messages[1].Role != domain.RoleAssistant {
		t.Fatalf("meta message roles lost: %+v", meta.Messages)
	}
	if meta.Messages[0].Content != "" || meta.Messages[0].ID != "" {
		t.Fatalf("meta projection must not carry message content: %+v", meta.Messages[0])
	}
	if !meta.HasDurableAnchor() {
		t.Fatal("HasDurableAnchor must stay truthful on a meta projection")
	}
	// A blob-anchored epoch with no user message must also report anchored.
	blob := newTestConversation("conv_meta_blob",
		domain.Message{ID: "msg_1", Role: domain.RoleAssistant, Content: "post-checkpoint"},
	)
	blob.CompactionBlob = `[{"type":"compaction","encrypted_content":"OPAQUE"}]`
	if err := store.Save(blob); err != nil {
		t.Fatalf("Save blob: %v", err)
	}
	found := false
	for _, m := range store.ListMeta() {
		if m.ID == "conv_meta_blob" {
			found = true
			if !m.HasDurableAnchor() {
				t.Fatal("blob-anchored conversation must keep HasDurableAnchor on the projection")
			}
		}
	}
	if !found {
		t.Fatal("conv_meta_blob missing")
	}

	// Mutating the projection — fields, slices, and the skeleton — must not
	// write into the store.
	meta.Title = "mutated"
	meta.PendingAnnouncements[0].Message = "mutated"
	meta.Messages[0].Role = domain.RoleAssistant
	fresh, err := store.Get("conv_meta_old")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Title != "older" || fresh.PendingAnnouncements[0].Message != "ping" || fresh.Messages[0].Role != domain.RoleUser {
		t.Fatal("mutating a ListMeta result must not affect the store")
	}
}

// TestListUsageProjectsAssistantUsage pins the telemetry projection: only
// assistant messages with usage are returned, each row is a flat private
// copy, and nothing is deep-cloned.
func TestListUsageProjectsAssistantUsage(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stamp := time.Now().UTC()
	conv := newTestConversation("conv_usage",
		domain.Message{ID: "msg_u", Role: domain.RoleUser, Content: "hi", CreatedAt: stamp},
		domain.Message{ID: "msg_a1", Role: domain.RoleAssistant, Content: "a1", Model: "prov_x:m1", ProviderID: "prov_x", CreatedAt: stamp, Usage: &domain.Usage{InputTokens: 10, OutputTokens: 5}},
		domain.Message{ID: "msg_a2", Role: domain.RoleAssistant, Content: "a2", CreatedAt: stamp}, // no usage
		domain.Message{ID: "msg_s", Role: domain.RoleSystem, Content: "sys", Usage: &domain.Usage{InputTokens: 1}},
	)
	other := newTestConversation("conv_usage2",
		domain.Message{ID: "msg_b1", Role: domain.RoleAssistant, Content: "b1", Model: "prov_y:m2", CreatedAt: stamp, Usage: &domain.Usage{InputTokens: 3, CacheRead: 7}},
	)
	for _, c := range []*domain.Conversation{conv, other} {
		if err := store.Save(c); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	rows := store.ListUsage()
	if len(rows) != 2 {
		t.Fatalf("ListUsage rows = %d, want 2 (assistant-with-usage only): %+v", len(rows), rows)
	}
	byConv := map[string]domain.UsageProjection{}
	for _, r := range rows {
		byConv[r.ConversationID] = r
	}
	r1, ok := byConv["conv_usage"]
	if !ok {
		t.Fatal("conv_usage row missing")
	}
	if r1.Model != "prov_x:m1" || r1.ProviderID != "prov_x" || !r1.CreatedAt.Equal(stamp) ||
		r1.Usage.InputTokens != 10 || r1.Usage.OutputTokens != 5 {
		t.Fatalf("row mismatch: %+v", r1)
	}
	if r2 := byConv["conv_usage2"]; r2.Usage.CacheRead != 7 {
		t.Fatalf("conv_usage2 row mismatch: %+v", r2)
	}
	// The returned Usage is a private copy.
	r1.Usage.InputTokens = -1
	if got, _ := store.Get("conv_usage"); got.Messages[1].Usage.InputTokens != 10 {
		t.Fatal("mutating a UsageProjection must not affect the store")
	}
}
