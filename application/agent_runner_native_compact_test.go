package application

import (
	"context"
	"strings"
	"sync"
	"testing"

	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

// nativeCompactorBody is a message body large enough that a handful of
// messages exceed the compaction keep budget (so there is a prefix to
// compact/archive). ~800 chars ≈ 200 tokens.
const nativeCompactorBody = "abcdefghij"

// nativeCompactorStub implements core.Provider and ServerCompactor. It records
// the CompactServer request and returns a canned blob, or an error when set.
type nativeCompactorStub struct {
	mu          sync.Mutex
	compactReqs []ChatRequest
	compactErr  error
	blob        string
}

func (s *nativeCompactorStub) Name() string { return "native-stub" }
func (s *nativeCompactorStub) Stream(context.Context, *core.Request) (core.Stream, error) {
	return nil, errNotFound
}
func (s *nativeCompactorStub) Chat(context.Context, *core.Request) (*core.Response, error) {
	return nil, errNotFound
}
func (s *nativeCompactorStub) CompactServer(_ context.Context, req ChatRequest) (CompactionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compactReqs = append(s.compactReqs, req)
	if s.compactErr != nil {
		return CompactionResult{}, s.compactErr
	}
	return CompactionResult{Blob: s.blob, InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, nil
}

func TestCompactConversationNativeSetsBlobAndKeepsSuffixIntact(t *testing.T) {
	// Build a conversation with a large prefix (archived) and a suffix that
	// carries tool calls (must survive intact — no StripForRetention).
	body := nativeBigBody() // ~200 tokens
	var msgs []domain.Message
	// Prefix: 10 large user messages that exceed the keep budget.
	for i := 0; i < 10; i++ {
		msgs = append(msgs, domain.Message{ID: "u" + itoa(i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone})
	}
	// Suffix: a recent user + assistant with a tool call that must survive intact.
	msgs = append(msgs, domain.Message{ID: "u_recent", Role: domain.RoleUser, Content: "recent question", Status: domain.StatusDone})
	msgs = append(msgs, domain.Message{
		ID: "a_recent", Role: domain.RoleAssistant, Content: "recent answer",
		Status:    domain.StatusDone,
		ToolCalls: []domain.ToolCall{{ID: "call_recent", Name: "shell", Args: `{"cmd":"ls"}`, Status: domain.ToolOK, Output: "recent output"}},
	})

	conv := &domain.Conversation{ID: "c-native", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c-native": conv}}
	stub := &nativeCompactorStub{blob: `[{"type":"compaction","encrypted_content":"ENC"}]`}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	settings := domain.DefaultSettings()

	summary, err := app.compactConversation(context.Background(), ProviderContext{Provider: stub, Kind: domain.ProviderResponses}, conv, "gpt-5.2", 4000, settings)
	if err != nil {
		t.Fatalf("compactConversation: %v", err)
	}
	if summary == "" {
		t.Fatal("summary should be non-empty marker for native compaction")
	}
	if conv.CompactionBlob != `[{"type":"compaction","encrypted_content":"ENC"}]` {
		t.Fatalf("CompactionBlob = %q, want the canned blob", conv.CompactionBlob)
	}
	if conv.Summary != "" {
		t.Fatalf("Summary = %q, want empty (blob replaces summary)", conv.Summary)
	}
	// The prefix (oldest user messages) must be archived with full content.
	if len(store.archived) == 0 {
		t.Fatal("prefix was not archived")
	}
	for _, m := range store.archived {
		if m.Content != body {
			t.Fatalf("archived message %q content was altered, want full body", m.ID)
		}
	}
	// No prefix user message should remain live.
	for _, m := range conv.Messages {
		if m.ID == "u0" {
			t.Fatal("prefix user u0 should have been archived, not kept live")
		}
	}
	// The suffix must keep the recent assistant tool call intact (no StripForRetention).
	var recent *domain.Message
	for i := range conv.Messages {
		if conv.Messages[i].ID == "a_recent" {
			recent = &conv.Messages[i]
		}
	}
	if recent == nil {
		t.Fatal("recent assistant message was dropped from the suffix")
	}
	if len(recent.ToolCalls) != 1 || recent.ToolCalls[0].ID != "call_recent" {
		t.Fatalf("suffix tool calls = %+v, want intact call_recent", recent.ToolCalls)
	}
	if recent.ToolCalls[0].Args != `{"cmd":"ls"}` {
		t.Fatalf("suffix tool call args = %q, want preserved", recent.ToolCalls[0].Args)
	}
	// CompactServer must have been called once with the prefix messages and the old (empty) blob.
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.compactReqs) != 1 {
		t.Fatalf("CompactServer calls = %d, want 1", len(stub.compactReqs))
	}
	if stub.compactReqs[0].CompactionBlob != "" {
		t.Fatalf("first compact CompactionBlob = %q, want empty (no prior blob)", stub.compactReqs[0].CompactionBlob)
	}
	if stub.compactReqs[0].Model != "gpt-5.2" {
		t.Fatalf("compact model = %q, want gpt-5.2", stub.compactReqs[0].Model)
	}
}

func TestCompactConversationNativeFallsBackOnCompactError(t *testing.T) {
	body := nativeBigBody()
	var msgs []domain.Message
	for i := 0; i < 10; i++ {
		msgs = append(msgs, domain.Message{ID: "u" + itoa(i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone})
	}
	msgs = append(msgs, domain.Message{ID: "u_recent", Role: domain.RoleUser, Content: "recent", Status: domain.StatusDone})

	conv := &domain.Conversation{ID: "c-fallback", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c-fallback": conv}}
	// Client-path provider: a recording adapter that returns a valid summary.
	clientAdapter := &recordingCompleteAdapter{toolCallSummaries: []string{validTestSummary}}
	// Use a composite stub that fails CompactServer but succeeds at Chat, so
	// the native path errors and the client-path fallback can complete.
	combined := &compositeNativeFallback{compactErr: errNotFound, chat: clientAdapter}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	settings := domain.DefaultSettings()
	settings.CompactionSummaryMaxTokens = 800

	summary, err := app.compactConversation(context.Background(), ProviderContext{Provider: combined, Kind: domain.ProviderResponses}, conv, "gpt-5.2", 4000, settings)
	if err != nil {
		t.Fatalf("compactConversation: %v", err)
	}
	if summary != validTestSummary {
		t.Fatalf("summary = %q, want client-path fallback summary", summary)
	}
	if conv.CompactionBlob != "" {
		t.Fatalf("CompactionBlob = %q, want empty after fallback to client path", conv.CompactionBlob)
	}
}

func TestCompactConversationNonEligibleModelSkipsNativePath(t *testing.T) {
	body := nativeBigBody()
	var msgs []domain.Message
	for i := 0; i < 10; i++ {
		msgs = append(msgs, domain.Message{ID: "u" + itoa(i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone})
	}
	msgs = append(msgs, domain.Message{ID: "u_recent", Role: domain.RoleUser, Content: "recent", Status: domain.StatusDone})

	conv := &domain.Conversation{ID: "c-noneligible", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c-noneligible": conv}}
	clientAdapter := &recordingCompleteAdapter{toolCallSummaries: []string{validTestSummary}}
	combined := &compositeNativeFallback{chat: clientAdapter}
	// combined implements ServerCompactor but the model is gpt-4o (not eligible).
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	settings := domain.DefaultSettings()
	settings.CompactionSummaryMaxTokens = 800

	summary, err := app.compactConversation(context.Background(), ProviderContext{Provider: combined, Kind: domain.ProviderResponses}, conv, "gpt-4o", 4000, settings)
	if err != nil {
		t.Fatalf("compactConversation: %v", err)
	}
	if summary != validTestSummary {
		t.Fatalf("summary = %q, want client-path summary", summary)
	}
	if conv.CompactionBlob != "" {
		t.Fatalf("CompactionBlob = %q, want empty (native path skipped)", conv.CompactionBlob)
	}
	combined.mu.Lock()
	if len(combined.compactReqs) != 0 {
		t.Fatalf("CompactServer calls = %d, want 0 for non-eligible model", len(combined.compactReqs))
	}
	combined.mu.Unlock()
}

// compositeNativeFallback implements core.Provider + ServerCompactor. It
// delegates Chat to a recording adapter (for the client-path fallback) and
// records CompactServer calls, returning compactErr when set.
type compositeNativeFallback struct {
	mu          sync.Mutex
	compactReqs []ChatRequest
	compactErr  error
	chat        *recordingCompleteAdapter
}

func (c *compositeNativeFallback) Name() string { return "composite" }
func (c *compositeNativeFallback) Stream(ctx context.Context, req *core.Request) (core.Stream, error) {
	return c.chat.Stream(ctx, req)
}
func (c *compositeNativeFallback) Chat(ctx context.Context, req *core.Request) (*core.Response, error) {
	return c.chat.Chat(ctx, req)
}
func (c *compositeNativeFallback) CompactServer(_ context.Context, req ChatRequest) (CompactionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.compactReqs = append(c.compactReqs, req)
	if c.compactErr != nil {
		return CompactionResult{}, c.compactErr
	}
	return CompactionResult{Blob: `[{"type":"compaction","encrypted_content":"ENC"}]`, InputTokens: 1, OutputTokens: 1, TotalTokens: 2}, nil
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// nativeBigBody returns a ~800-char body (~200 tokens) so a handful of
// messages exceeds the compaction keep budget.
func nativeBigBody() string { return strings.Repeat(nativeCompactorBody, 80) }
