package domain

import "testing"

// hydrationOrderingCheckpoint returns a pure hydration checkpoint message:
// assistant role, all tool calls carry the hydrate- prefix, no content or
// reasoning. Mirrors the shape FilterHydrationDomainMessages strips.
func hydrationOrderingCheckpoint(id string) Message {
	return Message{
		ID:   id,
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{ID: HydrateToolCallPrefix + "runtime_context", Name: "runtime_context", Status: ToolOK, Output: "{}"},
			{ID: HydrateToolCallPrefix + "todo_list", Name: "todo_list", Status: ToolOK, Output: "brief"},
		},
	}
}

func TestHydrationInsertIndex(t *testing.T) {
	cases := []struct {
		name string
		msgs []Message
		want int
	}{
		{"empty", nil, -1},
		{"no user", []Message{{ID: "a1", Role: RoleAssistant}}, -1},
		{"user first", []Message{{ID: "u1", Role: RoleUser}}, 1},
		{"user after assistant", []Message{{ID: "a1", Role: RoleAssistant}, {ID: "u1", Role: RoleUser}}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HydrationInsertIndex(tc.msgs); got != tc.want {
				t.Fatalf("HydrationInsertIndex = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestHydrationPrecedesFirstUser(t *testing.T) {
	cases := []struct {
		name string
		msgs []Message
		want bool
	}{
		{"empty", nil, false},
		{"user only", []Message{{ID: "u1", Role: RoleUser}}, false},
		{"hydration then user", []Message{hydrationOrderingCheckpoint("h1"), {ID: "u1", Role: RoleUser}}, true},
		{"user then hydration", []Message{{ID: "u1", Role: RoleUser}, hydrationOrderingCheckpoint("h1")}, false},
		{"assistant then user (no hydration)", []Message{{ID: "a1", Role: RoleAssistant}, {ID: "u1", Role: RoleUser}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HydrationPrecedesFirstUser(tc.msgs); got != tc.want {
				t.Fatalf("HydrationPrecedesFirstUser = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRelocateHydrationAfterFirstUserMovesCheckpoint(t *testing.T) {
	hyd := hydrationOrderingCheckpoint("h1")
	msgs := []Message{
		hyd,
		{ID: "u1", Role: RoleUser, Content: "halo", Status: StatusDone},
		{ID: "a1", Role: RoleAssistant, Content: "work", Status: StatusDone},
	}
	got := RelocateHydrationAfterFirstUser(msgs)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].ID != "u1" || !IsHydrationMessage(got[1]) || got[2].ID != "a1" {
		t.Fatalf("order = %s hyd=%v %s, want user, hydration, work", got[0].ID, IsHydrationMessage(got[1]), got[2].ID)
	}
	if got[1].ID != hyd.ID {
		t.Fatalf("checkpoint was rebuilt (id %s -> %s); repair must move the existing message", hyd.ID, got[1].ID)
	}
}

func TestRelocateHydrationAfterFirstUserNoOpWhenAlreadyAfterUser(t *testing.T) {
	msgs := []Message{
		{ID: "u1", Role: RoleUser, Content: "halo", Status: StatusDone},
		hydrationOrderingCheckpoint("h1"),
		{ID: "a1", Role: RoleAssistant, Content: "work", Status: StatusDone},
	}
	got := RelocateHydrationAfterFirstUser(msgs)
	if len(got) != 3 || got[0].ID != "u1" || !IsHydrationMessage(got[1]) || got[2].ID != "a1" {
		t.Fatalf("correct order must stay put: %+v", got)
	}
}

func TestRelocateHydrationAfterFirstUserDropsOrphanWhenNoUser(t *testing.T) {
	msgs := []Message{
		hydrationOrderingCheckpoint("h1"),
		{ID: "a1", Role: RoleAssistant, Content: "work", Status: StatusDone},
	}
	got := RelocateHydrationAfterFirstUser(msgs)
	// Orphan checkpoint with no user is dropped; only the assistant remains.
	if len(got) != 1 || got[0].ID != "a1" {
		t.Fatalf("orphan must be dropped when no user exists: %+v", got)
	}
}

func TestIsFreshRoom(t *testing.T) {
	cases := []struct {
		name string
		msgs []Message
		want bool
	}{
		{"empty", nil, true},
		{"one user", []Message{{ID: "u1", Role: RoleUser}}, true},
		{"two users", []Message{{ID: "u1", Role: RoleUser}, {ID: "u2", Role: RoleUser}}, false},
		{"assistant only", []Message{{ID: "a1", Role: RoleAssistant}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Conversation{ID: "c", Messages: tc.msgs}
			if got := IsFreshRoom(c); got != tc.want {
				t.Fatalf("IsFreshRoom = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsHydrationMessage(t *testing.T) {
	cases := []struct {
		name string
		m    Message
		want bool
	}{
		{"no tool calls", Message{ID: "a1", Role: RoleAssistant, Content: "hi"}, false},
		{"real tool call", Message{ID: "a1", Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Name: "file_read"}}}, false},
		{"hydration with content", Message{ID: "a1", Role: RoleAssistant, Content: "x", ToolCalls: []ToolCall{{ID: HydrateToolCallPrefix + "x", Name: "runtime_context"}}}, false},
		{"pure hydration", hydrationOrderingCheckpoint("h1"), true},
		{"mixed calls", Message{ID: "a1", Role: RoleAssistant, ToolCalls: []ToolCall{{ID: HydrateToolCallPrefix + "x", Name: "runtime_context"}, {ID: "call_1", Name: "file_read"}}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsHydrationMessage(tc.m); got != tc.want {
				t.Fatalf("IsHydrationMessage = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFilterHydrationToolCalls(t *testing.T) {
	t.Run("no tool calls returns unchanged", func(t *testing.T) {
		m := Message{ID: "a1", Role: RoleAssistant, Content: "hi"}
		got := FilterHydrationToolCalls(m)
		if got.Content != "hi" {
			t.Fatalf("unchanged message corrupted: %+v", got)
		}
	})
	t.Run("no hydration calls returns unchanged", func(t *testing.T) {
		m := Message{ID: "a1", Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Name: "file_read"}}}
		got := FilterHydrationToolCalls(m)
		if len(got.ToolCalls) != 1 || got.ToolCalls[0].ID != "call_1" {
			t.Fatalf("real-only calls corrupted: %+v", got)
		}
	})
	t.Run("mixed calls drops hydration only", func(t *testing.T) {
		m := Message{
			ID:   "a1",
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				{ID: HydrateToolCallPrefix + "x", Name: "runtime_context"},
				{ID: "call_1", Name: "file_read"},
			},
			Steps: []MessageStep{{
				Type:      StepToolCalls,
				ToolCalls: []ToolCall{{ID: HydrateToolCallPrefix + "x", Name: "runtime_context"}, {ID: "call_1", Name: "file_read"}},
			}},
		}
		got := FilterHydrationToolCalls(m)
		if len(got.ToolCalls) != 1 || got.ToolCalls[0].ID != "call_1" {
			t.Fatalf("hydration not stripped from ToolCalls: %+v", got.ToolCalls)
		}
		if len(got.Steps) != 1 || len(got.Steps[0].ToolCalls) != 1 || got.Steps[0].ToolCalls[0].ID != "call_1" {
			t.Fatalf("hydration not stripped from Steps: %+v", got.Steps)
		}
	})
}
