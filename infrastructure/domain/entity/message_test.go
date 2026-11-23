package entity

import (
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

func TestNewMessage(t *testing.T) {
	if _, err := NewMessage("", "x"); err == nil {
		t.Fatal("expected error for empty role")
	}
	m, err := NewMessage(enum.RoleUser, "hi")
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	if m.Role() != enum.RoleUser || m.Content() != "hi" {
		t.Fatalf("unexpected message: %+v", m)
	}
}

// The store hydrates role through ParseRole and refuses unknown values —
// the constructor cannot accept a value the record layer would reject:
// one bogus role persisted makes the whole room unloadable.
func TestNewMessageRejectsUnknownRole(t *testing.T) {
	if _, err := NewMessage(enum.Role("bogus"), "x"); err == nil {
		t.Fatal("constructor accepted a role hydration rejects")
	}
}

func TestMessageSetToolOutput(t *testing.T) {
	m, _ := NewMessage(enum.RoleAssistant, "")
	tc, _ := NewToolCall("call-1", "search", "{}", enum.TooltypeOrigin)
	m.AddToolCall(tc)

	if err := m.SetToolOutput("call-1", "ok"); err != nil {
		t.Fatalf("SetToolOutput: %v", err)
	}
	if got := m.ToolCalls()[0].Output(); !strings.Contains(got, "ok") {
		t.Fatalf("output not recorded: %q", got)
	}
	if err := m.SetToolOutput("missing", "x"); err == nil {
		t.Fatal("expected error for unknown call id")
	}
}

// SetToolOutput retains raw results; the budget applies to provider context.
func TestMessageSetToolOutputKeepsOverBudgetRawResult(t *testing.T) {
	m, _ := NewMessage(enum.RoleAssistant, "")
	tc, _ := NewToolCall("c", "n", "{}", enum.TooltypeOrigin)
	tc.SetOutputBudget(1)
	m.AddToolCall(tc)
	if err := m.SetToolOutput("c", "too long"); err != nil {
		t.Fatal(err)
	}
	got := m.ToolCalls()[0]
	if !got.NeedTruncate() || got.Output() != "too long" {
		t.Fatalf("raw output was rejected: %q", got.Output())
	}
}

// The call id is the only correlation key between a call and its
// output; two entries sharing one id make SetToolOutput ambiguous.
func TestMessageRejectsDuplicateToolCallID(t *testing.T) {
	m, _ := NewMessage(enum.RoleAssistant, "")
	tc, _ := NewToolCall("call-1", "search", "{}", enum.TooltypeOrigin)
	if err := m.AddToolCall(tc); err != nil {
		t.Fatalf("first call: %v", err)
	}
	dup, _ := NewToolCall("call-1", "other", "{}", enum.TooltypeOrigin)
	if err := m.AddToolCall(dup); err == nil {
		t.Fatal("duplicate tool call id accepted")
	}
	if len(m.ToolCalls()) != 1 {
		t.Fatalf("calls = %d, want 1", len(m.ToolCalls()))
	}
}

func TestMessageAttachments(t *testing.T) {
	m, _ := NewMessage(enum.RoleUser, "look")
	a, _ := valueobject.NewAttachment("https://x/i.png", "image/png")
	m.AddAttachment(a)
	if len(m.Attachments()) != 1 || m.Attachments()[0].Modality() != enum.ModalityImage {
		t.Fatalf("unexpected attachments: %+v", m.Attachments())
	}
}

func TestMediaPartRoundTrips(t *testing.T) {
	a, err := valueobject.NewAttachment("file:///data/uploads/u_1/chart.png", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	a.SetName("chart.png")
	a.SetSHA256("abc123")
	text := MessagePart{Kind: MessagePartText, Text: "here:"}
	m, _ := NewMessage(enum.RoleAssistant, "here:")
	m.SetParts([]MessagePart{text, NewMediaPart(a), {Kind: MessagePartMedia, Text: "not json"}})
	got := m.Media()
	if len(got) != 1 || got[0].URI() != a.URI() || got[0].Name() != "chart.png" || got[0].MediaType() != "image/png" || got[0].SHA256() != "abc123" {
		t.Fatalf("media = %+v", got)
	}
	if _, ok := text.Media(); ok {
		t.Fatal("a text part read as media")
	}
}

// A user message's recording rides on the entity and replays as a
// context note, not user text.
func TestUserMessageRecordingReplay(t *testing.T) {
	m, err := NewUserMessage(valueobject.UserInput{Text: "what happened",
		Recording: &valueobject.RecordingRef{ID: "rec_9", Status: "completed", Summary: "clicked through checkout"}})
	if err != nil {
		t.Fatal(err)
	}
	if m.Recording() == nil || m.Recording().ID != "rec_9" {
		t.Fatalf("recording = %+v", m.Recording())
	}
	replay := m.AsModelReplay()
	if !strings.Contains(replay.Content(), "<recording-context>") ||
		!strings.Contains(replay.Content(), "clicked through checkout") ||
		!strings.Contains(replay.Content(), "browser_get_timeline") {
		t.Fatalf("replay = %q", replay.Content())
	}
	if !strings.Contains(replay.Content(), "what happened") {
		t.Fatalf("text lost: %q", replay.Content())
	}
}
