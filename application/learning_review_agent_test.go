package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nusashell/domain"
	"nusashell/resources"
)

// reviewStubToolbox is a ToolExecutor stub for the review loop. The fail
// set causes Execute on those tool names to return an error, mimicking e.g.
// a model calling a tool that does not exist.
type reviewStubToolbox struct {
	fail map[string]bool
}

func (s *reviewStubToolbox) ListTools() []ToolInfo {
	return []ToolInfo{
		{Name: "memory_save", Description: "Save a fact"},
		{Name: "skill_save", Description: "Save a skill"},
	}
}

func (s *reviewStubToolbox) Execute(_ context.Context, name string, _ []byte) (string, error) {
	if s.fail[name] {
		return "", errors.New("boom: tool unavailable")
	}
	return "Saved " + name + " entry.", nil
}

// reviewStubAdapter is an AIProvider that returns tool calls for the first
// request and a terminal response afterwards. When terminalContent is set,
// the first call returns that content with no tool calls (simulating a
// "Nothing to save." response without any tool usage).
type reviewStubAdapter struct {
	toolCalls       []domain.ToolCall
	terminalContent string
	calls           int
}

func (a *reviewStubAdapter) Kind() domain.ProviderKind { return domain.ProviderChat }
func (a *reviewStubAdapter) Stream(context.Context, ChatRequest, func(string), func(string)) (ChatResponse, error) {
	return ChatResponse{}, nil
}

func (a *reviewStubAdapter) Complete(_ context.Context, req ChatRequest) (ChatResponse, error) {
	a.calls++
	if a.terminalContent != "" {
		return ChatResponse{Content: a.terminalContent}, nil
	}
	if a.calls == 1 {
		return ChatResponse{ToolCalls: a.toolCalls}, nil
	}
	return ChatResponse{Content: "Nothing to save."}, nil
}

func newReviewApp(toolbox *reviewStubToolbox) *App {
	return &App{
		Toolbox: toolbox,
		Logs:    &fakeLogStore{},
	}
}

func TestReviewLoopRecordsMutationOnlyOnSuccess(t *testing.T) {
	if resources.ReviewPrompt() == "" {
		t.Fatal("review prompt must be non-empty for the loop to run")
	}
	conv := &domain.Conversation{
		ID: "conv_1",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "please remember I prefer Indonesian"},
			{Role: domain.RoleAssistant, Content: "noted"},
		},
	}

	t.Run("successful save is recorded", func(t *testing.T) {
		agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), DefaultReviewSettings())
		adapter := &reviewStubAdapter{toolCalls: []domain.ToolCall{{
			Name: "memory_save",
			Args: `{"content":"user prefers Indonesian","tags":["preference","language"]}`,
		}}}
		mutations, _ := agent.runReviewLoop(context.Background(), adapter, "model", conv)
		if len(mutations) != 1 || mutations[0].Kind != "memory" {
			t.Fatalf("mutations = %+v, want exactly one memory mutation", mutations)
		}
		if mutations[0].Tool != "memory_save" {
			t.Errorf("mutation tool = %q, want memory_save", mutations[0].Tool)
		}
		if mutations[0].Snippet != "user prefers Indonesian" {
			t.Errorf("mutation snippet = %q, want content trimmed", mutations[0].Snippet)
		}
	})

	t.Run("skill mutation records name snippet", func(t *testing.T) {
		agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), DefaultReviewSettings())
		adapter := &reviewStubAdapter{toolCalls: []domain.ToolCall{{
			Name: "skill_save",
			Args: `{"name":"git-rebase-cheatsheet","content":"# Rebase\nsteps…"}`,
		}}}
		mutations, _ := agent.runReviewLoop(context.Background(), adapter, "model", conv)
		if len(mutations) != 1 || mutations[0].Kind != "skills" {
			t.Fatalf("mutations = %+v, want exactly one skills mutation", mutations)
		}
		if mutations[0].Snippet != "git-rebase-cheatsheet" {
			t.Errorf("mutation snippet = %q, want skill name", mutations[0].Snippet)
		}
	})

	t.Run("failed tool is not recorded as a mutation", func(t *testing.T) {
		agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{fail: map[string]bool{"memory_save": true}}), DefaultReviewSettings())
		adapter := &reviewStubAdapter{toolCalls: []domain.ToolCall{{
			Name: "memory_save",
			Args: `{"content":"x"}`,
		}}}
		mutations, _ := agent.runReviewLoop(context.Background(), adapter, "model", conv)
		if len(mutations) != 0 {
			t.Fatalf("mutations = %+v, want none when the tool fails", mutations)
		}
	})

	t.Run("non-whitelisted tool is not recorded", func(t *testing.T) {
		agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), DefaultReviewSettings())
		adapter := &reviewStubAdapter{toolCalls: []domain.ToolCall{{
			Name: "memory", // the (wrong) name referenced by the old prompt
			Args: `{"content":"x"}`,
		}}}
		mutations, _ := agent.runReviewLoop(context.Background(), adapter, "model", conv)
		if len(mutations) != 0 {
			t.Fatalf("mutations = %+v, want none for non-whitelisted tool", mutations)
		}
	})
}

// stubPrimaryStoreReview is a minimal PrimaryStore for review-agent tests.
type stubPrimaryStoreReview struct{ mem *domain.PrimaryMemory }

func (s *stubPrimaryStoreReview) Load() *domain.PrimaryMemory { return s.mem }
func (s *stubPrimaryStoreReview) Update(entries []domain.PrimaryEntry) error {
	return nil
}
func (s *stubPrimaryStoreReview) Replace(oldText, content string) error { return nil }

func TestReviewPromptInjectsPrimaryMemory(t *testing.T) {
	prompt := resources.ReviewPrompt()
	if prompt == "" {
		t.Fatal("review prompt must be non-empty")
	}
	app := &App{
		Toolbox: &reviewStubToolbox{},
		Logs:    &fakeLogStore{},
		Primary: &stubPrimaryStoreReview{mem: &domain.PrimaryMemory{
			Entries: []domain.PrimaryEntry{
				{ID: "prim_1", Content: "User prefers Indonesian"},
				{ID: "prim_2", Content: "Repo uses Go + Clean Architecture"},
			},
		}},
	}
	agent := NewBackgroundReviewAgent(app, DefaultReviewSettings())
	injected := agent.injectPrimaryMemory(prompt)
	if !strings.Contains(injected, "User prefers Indonesian") {
		t.Error("injected prompt should contain primary memory entry 'User prefers Indonesian'")
	}
	if !strings.Contains(injected, "Repo uses Go + Clean Architecture") {
		t.Error("injected prompt should contain primary memory entry 'Repo uses Go + Clean Architecture'")
	}
	if strings.Contains(injected, "{{primary_memory}}") {
		t.Error("placeholder should be replaced, not left in the prompt")
	}
}

func TestReviewPromptEmptyPrimaryMemory(t *testing.T) {
	prompt := resources.ReviewPrompt()
	app := &App{
		Toolbox: &reviewStubToolbox{},
		Logs:    &fakeLogStore{},
		Primary: &stubPrimaryStoreReview{mem: &domain.PrimaryMemory{Entries: nil}},
	}
	agent := NewBackgroundReviewAgent(app, DefaultReviewSettings())
	injected := agent.injectPrimaryMemory(prompt)
	if strings.Contains(injected, "{{primary_memory}}") {
		t.Error("placeholder should be replaced even when primary is empty")
	}
	if !strings.Contains(injected, "(empty)") {
		t.Error("empty primary should show '(empty)' marker")
	}
}

func TestReviewPromptNoPrimaryStore(t *testing.T) {
	prompt := resources.ReviewPrompt()
	app := &App{
		Toolbox: &reviewStubToolbox{},
		Logs:    &fakeLogStore{},
		// Primary not set
	}
	agent := NewBackgroundReviewAgent(app, DefaultReviewSettings())
	injected := agent.injectPrimaryMemory(prompt)
	if strings.Contains(injected, "{{primary_memory}}") {
		t.Error("placeholder should be replaced even when no PrimaryStore")
	}
	if !strings.Contains(injected, "(unavailable)") {
		t.Error("missing PrimaryStore should show '(unavailable)' marker")
	}
}

func TestBuildTranscriptIncludesToolCalls(t *testing.T) {
	app := &App{
		Toolbox: &reviewStubToolbox{},
		Logs:    &fakeLogStore{},
	}
	agent := NewBackgroundReviewAgent(app, DefaultReviewSettings())
	conv := &domain.Conversation{
		ID: "conv_tool",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "search the codebase for auth patterns"},
			{Role: domain.RoleAssistant, Content: "Let me search for auth patterns.", ToolCalls: []domain.ToolCall{
				{Name: "grep", Args: `{"pattern":"auth","path":"src/"}`, Output: "src/auth.go: 5 matches found"},
				{Name: "read_file", Args: `{"path":"src/auth.go"}`, Output: "package main\n\nfunc authenticate() {...}"},
			}},
			{Role: domain.RoleUser, Content: "great, now save what you found"},
		},
	}
	transcript := agent.buildTranscript(conv)
	if !strings.Contains(transcript, "→ tool: grep(") {
		t.Error("transcript should include tool call 'grep' with args")
	}
	if !strings.Contains(transcript, "result: src/auth.go: 5 matches found") {
		t.Error("transcript should include tool output for grep")
	}
	if !strings.Contains(transcript, "→ tool: read_file(") {
		t.Error("transcript should include tool call 'read_file' with args")
	}
	if !strings.Contains(transcript, "result: package main") {
		t.Error("transcript should include tool output for read_file")
	}
}

func TestBuildTranscriptTruncatesToolOutput(t *testing.T) {
	app := &App{
		Toolbox: &reviewStubToolbox{},
		Logs:    &fakeLogStore{},
	}
	agent := NewBackgroundReviewAgent(app, DefaultReviewSettings())
	longOutput := strings.Repeat("x", maxToolOutputChars+100)
	conv := &domain.Conversation{
		ID: "conv_trunc",
		Messages: []domain.Message{
			{Role: domain.RoleAssistant, ToolCalls: []domain.ToolCall{
				{Name: "read_file", Args: "{}", Output: longOutput},
			}},
		},
	}
	transcript := agent.buildTranscript(conv)
	if !strings.Contains(transcript, "…") {
		t.Error("transcript should truncate long tool output with ellipsis")
	}
}

func TestApplyReviewModelOverride(t *testing.T) {
	newApp := func(reviewModel string) *App {
		return &App{
			Providers: &fakeProviderStore{items: map[string]*domain.Provider{
				"chat-prov": {ID: "chat-prov", Enabled: true, Kind: domain.ProviderChat},
				"cheap-prov": {ID: "cheap-prov", Enabled: true, Kind: domain.ProviderChat, Models: []domain.Model{
					{ID: "haiku", Context: 128000},
				}},
			}},
			Credentials: &fakeVisionCredStore{creds: map[string]string{"cheap-prov": "key"}},
			Factory: func(_ context.Context, _ *domain.Provider, _ string) (AIProvider, error) {
				return &fakeVisionAdapter{description: "review-adapter"}, nil
			},
			Settings: &fakeSettingsStoreWithThreshold{threshold: 10, reviewModel: reviewModel},
			Logs:     &fakeLogStore{},
		}
	}

	t.Run("no override uses the conversation adapter", func(t *testing.T) {
		agent := NewBackgroundReviewAgent(newApp(""), DefaultReviewSettings())
		defaultAdapter := &fakeVisionAdapter{description: "default"}
		gotAdapter, gotModel := agent.applyReviewModelOverride(context.Background(), defaultAdapter, "gpt-5")
		if gotAdapter != defaultAdapter || gotModel != "gpt-5" {
			t.Fatalf("got (%v, %q), want default adapter+model", gotAdapter, gotModel)
		}
	})

	t.Run("configured override builds a separate adapter", func(t *testing.T) {
		agent := NewBackgroundReviewAgent(newApp("cheap-prov:haiku"), DefaultReviewSettings())
		defaultAdapter := &fakeVisionAdapter{description: "default"}
		gotAdapter, gotModel := agent.applyReviewModelOverride(context.Background(), defaultAdapter, "gpt-5")
		if gotAdapter == defaultAdapter {
			t.Fatal("expected a separate adapter for the review model override")
		}
		if gotModel != "haiku" {
			t.Fatalf("got model %q, want haiku", gotModel)
		}
	})

	t.Run("unresolvable override falls back", func(t *testing.T) {
		agent := NewBackgroundReviewAgent(newApp("nope:missing"), DefaultReviewSettings())
		defaultAdapter := &fakeVisionAdapter{description: "default"}
		gotAdapter, gotModel := agent.applyReviewModelOverride(context.Background(), defaultAdapter, "gpt-5")
		if gotAdapter != defaultAdapter || gotModel != "gpt-5" {
			t.Fatalf("got (%v, %q), want fallback to default adapter+model", gotAdapter, gotModel)
		}
	})

	t.Run("nil settings store falls back", func(t *testing.T) {
		agent := NewBackgroundReviewAgent(&App{Toolbox: &reviewStubToolbox{}, Logs: &fakeLogStore{}}, DefaultReviewSettings())
		defaultAdapter := &fakeVisionAdapter{description: "default"}
		gotAdapter, gotModel := agent.applyReviewModelOverride(context.Background(), defaultAdapter, "gpt-5")
		if gotAdapter != defaultAdapter || gotModel != "gpt-5" {
			t.Fatalf("got (%v, %q), want fallback when settings store is nil", gotAdapter, gotModel)
		}
	})
}

// chunkConversationStore returns a fixed archived chunk from GetChunk.
type chunkConversationStore struct {
	chunk []domain.Message
}

func (s *chunkConversationStore) Get(id string) (*domain.Conversation, error) { return nil, nil }
func (s *chunkConversationStore) Save(c *domain.Conversation) error           { return nil }
func (s *chunkConversationStore) List() []*domain.Conversation                { return nil }
func (s *chunkConversationStore) Delete(id string) error                      { return nil }
func (s *chunkConversationStore) ArchiveChunk(id string, messages []domain.Message) (int, error) {
	return 0, nil
}
func (s *chunkConversationStore) GetChunk(id string, index int) ([]domain.Message, error) {
	return s.chunk, nil
}

func TestBuildTranscriptTokenCapDropsOldest(t *testing.T) {
	app := &App{
		Toolbox: &reviewStubToolbox{},
		Logs:    &fakeLogStore{},
	}
	settings := DefaultReviewSettings()
	settings.MaxTranscriptTokens = 10 // 40 chars cap
	agent := NewBackgroundReviewAgent(app, settings)
	long := strings.Repeat("y", 100)
	conv := &domain.Conversation{
		ID: "conv_cap",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "OLDEST-" + long},
			{Role: domain.RoleAssistant, Content: "NEWEST-" + long},
		},
	}
	transcript := agent.buildTranscript(conv)
	if strings.Contains(transcript, "OLDEST") {
		t.Error("transcript should drop the oldest line when over the token cap")
	}
	if !strings.Contains(transcript, "NEWEST") {
		t.Error("transcript should keep the most recent line")
	}
}

func TestBuildTranscriptUsesArchivedChunkWhenCompacted(t *testing.T) {
	app := &App{
		Toolbox: &reviewStubToolbox{},
		Logs:    &fakeLogStore{},
		Conversations: &chunkConversationStore{chunk: []domain.Message{
			{Role: domain.RoleAssistant, Content: "old work", ToolCalls: []domain.ToolCall{
				{Name: "grep", Args: `{"pattern":"auth"}`, Output: "src/auth.go: 5 matches"},
			}},
		}},
	}
	agent := NewBackgroundReviewAgent(app, DefaultReviewSettings())
	// Post-compaction live messages have tool calls stripped (StripForRetention).
	conv := &domain.Conversation{
		ID:         "conv_compacted",
		ChunkCount: 1,
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "recent question"},
			{Role: domain.RoleAssistant, Content: "recent answer"},
		},
	}
	transcript := agent.buildTranscript(conv)
	if !strings.Contains(transcript, "→ tool: grep(") {
		t.Error("transcript should include tool calls from the archived chunk")
	}
	if !strings.Contains(transcript, "recent question") {
		t.Error("transcript should still include live messages")
	}
}

func TestIsNothingToSave(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"Nothing to save.", true},
		{"nothing to save.", true},
		{"NOTHING TO SAVE", true},
		{"  Nothing to save.  ", true},
		{"Nothing to save. The conversation was casual chit-chat.", true},
		{"I reviewed the transcript. Nothing to save — just greetings.", true},
		{"", false},
		{"Saved a memory entry about user preferences.", false},
		{"Let me search for existing fragments first.", false},
	}
	for _, c := range cases {
		got := isNothingToSave(c.input)
		if got != c.want {
			t.Errorf("isNothingToSave(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestReviewLoopEndsOnNothingToSave(t *testing.T) {
	if resources.ReviewPrompt() == "" {
		t.Fatal("review prompt must be non-empty")
	}
	app := &App{
		Toolbox: &reviewStubToolbox{},
		Logs:    &fakeLogStore{},
	}
	agent := NewBackgroundReviewAgent(app, DefaultReviewSettings())
	conv := &domain.Conversation{
		ID: "conv_chitchat",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "hi, how are you?"},
			{Role: domain.RoleAssistant, Content: "I'm doing well, thanks!"},
		},
	}
	// Adapter returns "Nothing to save." on the first call — the loop
	// should stop immediately without executing any tool.
	adapter := &reviewStubAdapter{
		terminalContent: "Nothing to save.",
	}
	mutations, messages := agent.runReviewLoop(context.Background(), adapter, "model", conv)
	if len(mutations) != 0 {
		t.Errorf("expected 0 mutations for chit-chat, got %d", len(mutations))
	}
	// The loop breaks before appending the "Nothing to save." response,
	// so messages should contain only the initial transcript user message.
	if len(messages) != 1 {
		t.Errorf("expected 1 message (transcript only), got %d", len(messages))
	}
	// Verify the adapter was called exactly once (no tool rounds).
	if adapter.calls != 1 {
		t.Errorf("expected 1 LLM call, got %d", adapter.calls)
	}
}
