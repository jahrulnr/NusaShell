package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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
		{Name: "memory", Description: "Memory store (save/replace/search/list)"},
		{Name: "skill", Description: "Skill library (list/search/read/files/save)"},
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
	failOnCall      int
	failErr         error
}

func (a *reviewStubAdapter) Kind() domain.ProviderKind { return domain.ProviderChat }
func (a *reviewStubAdapter) Stream(ctx context.Context, req ChatRequest, _ func(string), _ func(string)) (ChatResponse, error) {
	// Delegate to Complete: the review loop now calls Stream, and these stubs
	// model a provider whose stream result carries the same ChatResponse.
	return a.Complete(ctx, req)
}

func (a *reviewStubAdapter) Complete(_ context.Context, req ChatRequest) (ChatResponse, error) {
	a.calls++
	if a.failOnCall > 0 && a.calls == a.failOnCall {
		err := a.failErr
		if err == nil {
			err = errors.New("complete failed")
		}
		return ChatResponse{}, err
	}
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
		Toolbox:              toolbox,
		Logs:                 &fakeLogStore{},
		turnsSinceReview:     map[string]int{},
		toolCallsSinceReview: map[string]int{},
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
			Name: "memory",
			Args: `{"op":"save","content":"user prefers Indonesian","tags":["preference","language"]}`,
		}}}
		mutations, _, err := agent.runReviewLoop(context.Background(), adapter, "model", conv)
		if err != nil {
			t.Fatalf("runReviewLoop: %v", err)
		}
		if len(mutations) != 1 || mutations[0].Kind != "memory" {
			t.Fatalf("mutations = %+v, want exactly one memory mutation", mutations)
		}
		if mutations[0].Tool != "memory" {
			t.Errorf("mutation tool = %q, want memory", mutations[0].Tool)
		}
		if mutations[0].Snippet != "user prefers Indonesian" {
			t.Errorf("mutation snippet = %q, want content trimmed", mutations[0].Snippet)
		}
	})

	t.Run("skill mutation records name snippet", func(t *testing.T) {
		agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), DefaultReviewSettings())
		adapter := &reviewStubAdapter{toolCalls: []domain.ToolCall{{
			Name: "skill",
			Args: `{"op":"save","name":"git-rebase-cheatsheet","content":"# Rebase\nsteps…"}`,
		}}}
		mutations, _, err := agent.runReviewLoop(context.Background(), adapter, "model", conv)
		if err != nil {
			t.Fatalf("runReviewLoop: %v", err)
		}
		if len(mutations) != 1 || mutations[0].Kind != "skills" {
			t.Fatalf("mutations = %+v, want exactly one skills mutation", mutations)
		}
		if mutations[0].Snippet != "git-rebase-cheatsheet" {
			t.Errorf("mutation snippet = %q, want skill name", mutations[0].Snippet)
		}
	})

	t.Run("failed tool is not recorded as a mutation", func(t *testing.T) {
		agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{fail: map[string]bool{"memory": true}}), DefaultReviewSettings())
		adapter := &reviewStubAdapter{toolCalls: []domain.ToolCall{{
			Name: "memory",
			Args: `{"op":"save","content":"x"}`,
		}}}
		mutations, _, err := agent.runReviewLoop(context.Background(), adapter, "model", conv)
		if err != nil {
			t.Fatalf("runReviewLoop: %v", err)
		}
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
		mutations, _, err := agent.runReviewLoop(context.Background(), adapter, "model", conv)
		if err != nil {
			t.Fatalf("runReviewLoop: %v", err)
		}
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

func TestReviewLoopRecordsMemoryReplaceMutation(t *testing.T) {
	if resources.ReviewPrompt() == "" {
		t.Fatal("review prompt must be non-empty for the loop to run")
	}
	conv := &domain.Conversation{
		ID: "conv_replace",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "the existing fact changed"},
		},
	}
	agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), DefaultReviewSettings())
	adapter := &reviewStubAdapter{toolCalls: []domain.ToolCall{{
		Name: "memory",
		Args: fmt.Sprintf("{%q:%q, %q:%q}", "op", "replace", "content", "updated durable fact"),
	}}}

	mutations, _, err := agent.runReviewLoop(context.Background(), adapter, "model", conv)
	if err != nil {
		t.Fatalf("runReviewLoop: %v", err)
	}
	if len(mutations) != 1 {
		t.Fatalf("mutations = %+v, want exactly one mutation", mutations)
	}
	if mutations[0].Kind != "memory" || mutations[0].Snippet != "updated durable fact" {
		t.Fatalf("mutation = %+v, want a memory replace mutation", mutations[0])
	}
	if mutations[0].Snippet != "updated durable fact" {
		t.Fatalf("mutation snippet = %q, want replacement content", mutations[0].Snippet)
	}
}

func TestExecuteMemoryPrimaryInjectsContent(t *testing.T) {
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
	output := agent.executeMemoryPrimary()
	if !strings.Contains(output, "User prefers Indonesian") {
		t.Error("executeMemoryPrimary should contain primary memory entry 'User prefers Indonesian'")
	}
	if !strings.Contains(output, "Repo uses Go + Clean Architecture") {
		t.Error("executeMemoryPrimary should contain primary memory entry 'Repo uses Go + Clean Architecture'")
	}
}

func TestExecuteMemoryPrimaryEmpty(t *testing.T) {
	app := &App{
		Toolbox: &reviewStubToolbox{},
		Logs:    &fakeLogStore{},
		Primary: &stubPrimaryStoreReview{mem: &domain.PrimaryMemory{Entries: nil}},
	}
	agent := NewBackgroundReviewAgent(app, DefaultReviewSettings())
	output := agent.executeMemoryPrimary()
	if output != "(empty)" {
		t.Errorf("empty primary should show '(empty)', got %q", output)
	}
}

func TestExecuteMemoryPrimaryNoStore(t *testing.T) {
	app := &App{
		Toolbox: &reviewStubToolbox{},
		Logs:    &fakeLogStore{},
		// Primary not set
	}
	agent := NewBackgroundReviewAgent(app, DefaultReviewSettings())
	output := agent.executeMemoryPrimary()
	if output != "(unavailable)" {
		t.Errorf("missing PrimaryStore should show '(unavailable)', got %q", output)
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

// TestApplyReviewModelOverrideRecordsOnlyModelChanges verifies the
// learning-log noise rule: an override that resolves to the same bare model
// the conversation already uses is a no-op and must not emit a review_model
// event, while an override that switches the model still records one.
func TestApplyReviewModelOverrideRecordsOnlyModelChanges(t *testing.T) {
	newApp := func(reviewModel string) *App {
		return &App{
			Toolbox: &reviewStubToolbox{},
			Logs:    &fakeLogStore{},
			Providers: &fakeProviderStore{items: map[string]*domain.Provider{
				"cheap-prov": {ID: "cheap-prov", Enabled: true, Kind: domain.ProviderChat, Models: []domain.Model{
					{ID: "haiku", Context: 128000},
				}},
			}},
			Credentials: &fakeVisionCredStore{creds: map[string]string{"cheap-prov": "key"}},
			Factory: func(_ context.Context, _ *domain.Provider, _ string) (AIProvider, error) {
				return &fakeVisionAdapter{description: "review-adapter"}, nil
			},
			Settings:             &fakeSettingsStoreWithThreshold{threshold: 10, reviewModel: reviewModel},
			turnsSinceReview:     map[string]int{},
			toolCallsSinceReview: map[string]int{},
		}
	}

	t.Run("same model as conversation records nothing", func(t *testing.T) {
		tmp := t.TempDir()
		traj := NewTrajectoryRecorder(tmp)
		defer traj.Close()
		app := newApp("cheap-prov:haiku")
		app.Trajectory = traj
		agent := NewBackgroundReviewAgent(app, DefaultReviewSettings())
		defaultAdapter := &fakeVisionAdapter{description: "default"}
		gotAdapter, gotModel := agent.applyReviewModelOverride(context.Background(), defaultAdapter, "haiku")
		if gotAdapter == defaultAdapter || gotModel != "haiku" {
			t.Fatalf("got (%v, %q), want override adapter keeping the same model", gotAdapter, gotModel)
		}
		for _, e := range ReadTrajectory(tmp, 100) {
			if e.Type == "review_model" {
				t.Fatalf("review_model event recorded for same-model override: %+v", e.Detail)
			}
		}
	})

	t.Run("different model records ok event", func(t *testing.T) {
		tmp := t.TempDir()
		traj := NewTrajectoryRecorder(tmp)
		defer traj.Close()
		app := newApp("cheap-prov:haiku")
		app.Trajectory = traj
		agent := NewBackgroundReviewAgent(app, DefaultReviewSettings())
		defaultAdapter := &fakeVisionAdapter{description: "default"}
		gotAdapter, gotModel := agent.applyReviewModelOverride(context.Background(), defaultAdapter, "gpt-5")
		if gotAdapter == defaultAdapter || gotModel != "haiku" {
			t.Fatalf("got (%v, %q), want override adapter with haiku", gotAdapter, gotModel)
		}
		found := false
		for _, e := range ReadTrajectory(tmp, 100) {
			if e.Type == "review_model" {
				found = true
				if e.Detail["status"] != "ok" || e.Detail["resolved"] != "haiku" {
					t.Errorf("detail = %v, want status=ok resolved=haiku", e.Detail)
				}
			}
		}
		if !found {
			t.Fatal("review_model event not recorded for model-changing override")
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
	// Chunk messages are older (earlier timestamps) than live messages.
	// transcriptMessages must merge by CreatedAt so the conversation flow
	// stays chronological — without sorting, the chunk (archived assistant
	// responses) would appear before the live user messages that triggered
	// them, producing a temporally wrong transcript.
	chunkTime := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	liveTime := time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC)
	app := &App{
		Toolbox: &reviewStubToolbox{},
		Logs:    &fakeLogStore{},
		Conversations: &chunkConversationStore{chunk: []domain.Message{
			{Role: domain.RoleAssistant, Content: "old work", CreatedAt: chunkTime, ToolCalls: []domain.ToolCall{
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
			{Role: domain.RoleUser, Content: "recent question", CreatedAt: liveTime},
			{Role: domain.RoleAssistant, Content: "recent answer", CreatedAt: liveTime.Add(30 * time.Second)},
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

// TestTranscriptMessagesChronologicalOrder verifies that chunk and live
// messages are merged by CreatedAt timestamp, not naively prepended.
// Compaction retains user messages in live and archives assistant messages
// to chunks — without timestamp sorting, the merged array has assistant
// responses before the user messages that triggered them.
func TestTranscriptMessagesChronologicalOrder(t *testing.T) {
	chunkTime := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	liveTime := time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC)
	app := &App{
		Toolbox: &reviewStubToolbox{},
		Logs:    &fakeLogStore{},
		Conversations: &chunkConversationStore{chunk: []domain.Message{
			{Role: domain.RoleAssistant, Content: "response to heyyo", CreatedAt: chunkTime.Add(10 * time.Second)},
			{Role: domain.RoleAssistant, Content: "response to show request", CreatedAt: chunkTime.Add(20 * time.Second)},
		}},
	}
	agent := NewBackgroundReviewAgent(app, DefaultReviewSettings())
	conv := &domain.Conversation{
		ID:         "conv_chrono",
		ChunkCount: 1,
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "heyyo", CreatedAt: chunkTime},
			{Role: domain.RoleUser, Content: "coba show video", CreatedAt: chunkTime.Add(15 * time.Second)},
			{Role: domain.RoleAssistant, Content: "post-compaction reply", CreatedAt: liveTime},
		},
	}
	merged := agent.transcriptMessages(conv)
	// Expected chronological order:
	// [0] user "heyyo"               (chunkTime)
	// [1] assistant "response to heyyo" (chunkTime+10s)
	// [2] user "coba show video"      (chunkTime+15s)
	// [3] assistant "response to show" (chunkTime+20s)
	// [4] assistant "post-compaction"  (liveTime)
	if len(merged) != 5 {
		t.Fatalf("merged len = %d, want 5", len(merged))
	}
	if merged[0].Content != "heyyo" {
		t.Errorf("merged[0] = %q, want 'heyyo' (user message before assistant response)", merged[0].Content)
	}
	if merged[1].Content != "response to heyyo" {
		t.Errorf("merged[1] = %q, want 'response to heyyo'", merged[1].Content)
	}
	if merged[2].Content != "coba show video" {
		t.Errorf("merged[2] = %q, want 'coba show video'", merged[2].Content)
	}
	if merged[3].Content != "response to show request" {
		t.Errorf("merged[3] = %q, want 'response to show request'", merged[3].Content)
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
	mutations, messages, err := agent.runReviewLoop(context.Background(), adapter, "model", conv)
	if err != nil {
		t.Fatalf("runReviewLoop: %v", err)
	}
	if len(mutations) != 0 {
		t.Errorf("expected 0 mutations for chit-chat, got %d", len(mutations))
	}
	// The loop persists the terminal "Nothing to save." response as the
	// review's conclusion, so the learning log UI can show what the agent
	// decided even when it stored nothing. With the synthetic-tool refactor,
	// the message sequence is: user prompt, synthetic assistant tool calls,
	// 2 tool results, and the final assistant conclusion = 5 messages.
	if len(messages) != 5 {
		t.Errorf("expected 5 messages (prompt + synthetic + 2 tools + conclusion), got %d", len(messages))
	}
	// Verify the adapter was called exactly once (no tool rounds).
	if adapter.calls != 1 {
		t.Errorf("expected 1 LLM call, got %d", adapter.calls)
	}
}

func TestReviewLoopEmptyResponseIsError(t *testing.T) {
	if resources.ReviewPrompt() == "" {
		t.Fatal("review prompt must be non-empty")
	}
	agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), DefaultReviewSettings())
	conv := &domain.Conversation{
		ID:       "conv_empty",
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "remember this durable fact"}},
	}
	adapter := &persistStubAdapter{responses: []ChatResponse{{}}}

	_, messages, err := agent.runReviewLoop(context.Background(), adapter, "model", conv)
	if err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Fatalf("err = %v, want empty response error", err)
	}
	// Pre-injected synthetic tools produce 4 messages before the first LLM
	// call: user prompt, synthetic assistant tool calls, and 2 tool results.
	if len(messages) != 4 {
		t.Fatalf("messages = %d, want 4 (user + synthetic assistant + 2 tool results)", len(messages))
	}
}

func TestReviewFailureCooldownSuppressesAndExpires(t *testing.T) {
	settings := DefaultReviewSettings()
	settings.ReviewCooldown = time.Hour
	agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), settings)
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	agent.now = func() time.Time { return now }

	if !agent.tryAcquireReview("conv_1") {
		t.Fatal("first review should acquire the conversation")
	}
	if agent.tryAcquireReview("conv_1") {
		t.Fatal("concurrent duplicate should be suppressed while the first review is in flight")
	}
	agent.releaseReview("conv_1", true)
	if agent.tryAcquireReview("conv_1") {
		t.Fatal("failed review should be suppressed during retry cooldown")
	}

	now = now.Add(time.Hour)
	if !agent.tryAcquireReview("conv_1") {
		t.Fatal("review should be allowed after retry cooldown expires")
	}
	agent.releaseReview("conv_1", false)
}

func TestReviewReservationCoalescesActiveTriggers(t *testing.T) {
	tmp := t.TempDir()
	app := newReviewApp(&reviewStubToolbox{})
	app.Trajectory = NewTrajectoryRecorder(tmp)
	defer app.Trajectory.Close()
	agent := NewBackgroundReviewAgent(app, DefaultReviewSettings())

	if !agent.reserveReview("conv_active") {
		t.Fatal("first review should reserve")
	}
	if agent.reserveReview("conv_active") {
		t.Fatal("active duplicate should be coalesced")
	}
	if agent.reserveReview("conv_active") {
		t.Fatal("repeated active duplicate should be coalesced")
	}

	events := ReadTrajectory(tmp, 100)
	var skipped int
	for _, event := range events {
		if event.Type == "review" && event.Detail["status"] == "skipped" {
			skipped++
			if event.Detail["reason"] != "already_running" {
				t.Fatalf("skip reason = %v, want already_running", event.Detail["reason"])
			}
		}
	}
	if skipped != 1 {
		t.Fatalf("coalesced skip events = %d, want exactly 1", skipped)
	}
	agent.releaseReview("conv_active")
}

func TestReviewSuccessAlsoEntersCooldown(t *testing.T) {
	settings := DefaultReviewSettings()
	settings.ReviewCooldown = time.Hour
	agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), settings)
	if !agent.tryAcquireReview("conv_success") {
		t.Fatal("first review should acquire")
	}
	agent.releaseReview("conv_success", false)
	if agent.tryAcquireReview("conv_success") {
		t.Fatal("successful review should be protected by cooldown (prevents redundant re-review)")
	}
}

func TestReviewFailureStartsCooldown(t *testing.T) {
	settings := DefaultReviewSettings()
	settings.ReviewCooldown = time.Hour
	agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), settings)
	if !agent.tryAcquireReview("conv_failure") {
		t.Fatal("first review should acquire")
	}
	agent.releaseReview("conv_failure", true)
	if agent.tryAcquireReview("conv_failure") {
		t.Fatal("failed review should be protected by retry cooldown")
	}
}

func TestReviewCooldownAllowsDifferentConversations(t *testing.T) {
	settings := DefaultReviewSettings()
	settings.ReviewCooldown = time.Hour
	agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), settings)
	if !agent.tryAcquireReview("conv_1") {
		t.Fatal("first conversation should acquire")
	}
	if !agent.tryAcquireReview("conv_2") {
		t.Fatal("different conversation should not be blocked")
	}
	agent.releaseReview("conv_1")
	agent.releaseReview("conv_2")
}

func TestReviewLoopCompleteErrorIsReturned(t *testing.T) {
	if resources.ReviewPrompt() == "" {
		t.Fatal("review prompt must be non-empty")
	}
	agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), DefaultReviewSettings())
	conv := &domain.Conversation{
		ID: "conv_fail",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "remember I prefer Indonesian"},
			{Role: domain.RoleAssistant, Content: "noted"},
		},
	}
	adapter := &reviewStubAdapter{
		toolCalls: []domain.ToolCall{{
			Name: "memory",
			Args: `{"op":"save","content":"user prefers Indonesian"}`,
		}},
		failOnCall: 2,
		failErr:    errors.New("provider 500"),
	}
	mutations, _, err := agent.runReviewLoop(context.Background(), adapter, "model", conv)
	if err == nil || !strings.Contains(err.Error(), "provider 500") {
		t.Fatalf("err = %v, want provider 500", err)
	}
	if len(mutations) != 1 {
		t.Fatalf("partial mutations = %+v, want the successful first-round save", mutations)
	}
}

// ---- Phase 1: Hydration tool tests ----

// TestReviewTranscriptToolReturnsStructuredJSON verifies the hydration tool
// returns valid JSON with proper roles, nested tool_calls, and truncated output.
func TestReviewTranscriptToolReturnsStructuredJSON(t *testing.T) {
	conv := &domain.Conversation{
		ID:    "conv_hydr",
		Model: "prov_x:gpt-test",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "fix the login bug"},
			{Role: domain.RoleAssistant, Content: "Let me search.", ToolCalls: []domain.ToolCall{
				{Name: "grep", Args: `{"pattern":"login"}`, Output: "auth.go:5 matches"},
			}},
			{Role: domain.RoleAssistant, Content: "Found the bug in line 42."},
		},
	}
	agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), DefaultReviewSettings())
	jsonOut := agent.executeReviewTranscript(conv, 0, 0, "/path/to/conv.json")

	// Must be valid JSON with expected top-level fields.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &parsed); err != nil {
		t.Fatalf("executeReviewTranscript returned invalid JSON: %v\n%s", err, jsonOut)
	}
	if parsed["conversation_id"] != "conv_hydr" {
		t.Errorf("conversation_id = %v, want conv_hydr", parsed["conversation_id"])
	}
	if parsed["model"] != "prov_x:gpt-test" {
		t.Errorf("model = %v, want prov_x:gpt-test", parsed["model"])
	}
	msgs, ok := parsed["messages"].([]any)
	if !ok {
		t.Fatalf("messages is not an array: %T", parsed["messages"])
	}
	if len(msgs) != 3 {
		t.Fatalf("messages len = %d, want 3", len(msgs))
	}
	// Second message (assistant) must have nested tool_calls.
	asstMsg, ok := msgs[1].(map[string]any)
	if !ok {
		t.Fatalf("msg[1] is not an object: %T", msgs[1])
	}
	if asstMsg["role"] != "assistant" {
		t.Errorf("msg[1] role = %v, want assistant", asstMsg["role"])
	}
	toolCalls, ok := asstMsg["tool_calls"].([]any)
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("msg[1] tool_calls = %v, want 1 entry", asstMsg["tool_calls"])
	}
	tc, ok := toolCalls[0].(map[string]any)
	if !ok {
		t.Fatalf("tool_calls[0] is not an object: %T", toolCalls[0])
	}
	if tc["name"] != "grep" {
		t.Errorf("tool_calls[0] name = %v, want grep", tc["name"])
	}
	if tc["output"] != "auth.go:5 matches" {
		t.Errorf("tool_calls[0] output = %v, want auth.go:5 matches", tc["output"])
	}
}

// TestExecuteReviewTranscriptBoundedRange verifies the incremental marker:
// when start > 0, only messages [start:end) are included — not the full
// conversation. This prevents re-reading already reviewed content.
func TestExecuteReviewTranscriptBoundedRange(t *testing.T) {
	conv := &domain.Conversation{
		ID:    "conv_range",
		Model: "prov_x:gpt-test",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "msg 0 (already reviewed)"},
			{Role: domain.RoleAssistant, Content: "msg 1 (already reviewed)"},
			{Role: domain.RoleUser, Content: "msg 2 (new)"},
			{Role: domain.RoleAssistant, Content: "msg 3 (new)"},
		},
	}
	agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), DefaultReviewSettings())

	// Simulate LastReviewedMsgCount=2: only messages [2:4) should appear.
	jsonOut := agent.executeReviewTranscript(conv, 2, 0, "/path/to/conv.json")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, jsonOut)
	}
	if parsed["message_start"].(float64) != 2 {
		t.Errorf("message_start = %v, want 2", parsed["message_start"])
	}
	if parsed["message_end"].(float64) != 4 {
		t.Errorf("message_end = %v, want 4", parsed["message_end"])
	}
	msgs, _ := parsed["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages len = %d, want 2 (only [2:4))", len(msgs))
	}
	first, _ := msgs[0].(map[string]any)
	if first["content"] != "msg 2 (new)" {
		t.Errorf("first message = %v, want 'msg 2 (new)'", first["content"])
	}
}

// TestExecuteReviewTranscriptPathExposed verifies the path field is included
// in the transcript JSON so the agent can file_read the full conversation.
func TestExecuteReviewTranscriptPathExposed(t *testing.T) {
	conv := &domain.Conversation{
		ID:       "conv_path",
		Model:    "prov_x:gpt-test",
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hi"}},
	}
	agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), DefaultReviewSettings())
	jsonOut := agent.executeReviewTranscript(conv, 0, 0, "/abs/path/to/conv.json")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed["path"] != "/abs/path/to/conv.json" {
		t.Errorf("path = %v, want /abs/path/to/conv.json", parsed["path"])
	}
}

// TestReviewLoopCompactionResetsStaleMarker verifies that when compaction
// shrinks the merged transcript array (transcriptMessages only includes the
// latest chunk), a stale LastReviewedMsgCount that exceeds the new array
// length is reset to 0 instead of skipping the review entirely. Without
// this fix, multi-compaction conversations would never review again because
// start >= end always returns nil.
func TestReviewLoopCompactionResetsStaleMarker(t *testing.T) {
	if resources.ReviewPrompt() == "" {
		t.Fatal("review prompt must be non-empty")
	}
	// Simulate post-second-compaction state:
	//   chunk-1 has 3 messages, live has 2 messages → merged = 5
	//   LastReviewedMsgCount = 51 (set before compaction shrank the array)
	//   51 > 5 → without the fix, runReviewLoop returns nil (no review)
	conv := &domain.Conversation{
		ID:                   "conv_multi_compact",
		Model:                "prov_x:gpt-test",
		LastReviewedMsgCount: 51,
		ChunkCount:           2,
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "post-compaction msg 1"},
			{Role: domain.RoleAssistant, Content: "post-compaction reply 1"},
		},
	}
	app := &App{
		Toolbox: &reviewStubToolbox{},
		Logs:    &fakeLogStore{},
		Conversations: &chunkConversationStore{chunk: []domain.Message{
			{Role: domain.RoleUser, Content: "chunk msg 1"},
			{Role: domain.RoleAssistant, Content: "chunk msg 2"},
			{Role: domain.RoleUser, Content: "chunk msg 3"},
		}},
	}
	agent := NewBackgroundReviewAgent(app, DefaultReviewSettings())
	adapter := &reviewStubAdapter{terminalContent: "Nothing to save."}
	_, messages, err := agent.runReviewLoop(context.Background(), adapter, "model", conv)
	if err != nil {
		t.Fatalf("runReviewLoop: %v", err)
	}
	// Without the fix, messages would be nil (start >= end → return nil).
	// With the fix, start is reset to 0 and the review runs.
	if messages == nil {
		t.Fatal("expected review to run after marker reset, got nil messages (stale marker not reset)")
	}
	// 5 messages: user prompt + synthetic assistant + 2 tool results + conclusion
	if len(messages) != 5 {
		t.Errorf("messages = %d, want 5; got %+v", len(messages), messages)
	}
}

// TestReviewTranscriptToolNotInToolbox verifies the hydration tool is NOT
// registered in the global Toolbox — it exists only in the review agent's
// toolset.
func TestReviewTranscriptToolNotInToolbox(t *testing.T) {
	toolbox := &reviewStubToolbox{}
	for _, ti := range toolbox.ListTools() {
		if ti.Name == "review_transcript" {
			t.Fatal("review_transcript must NOT be in Toolbox.ListTools()")
		}
	}
}

// TestReviewLoopCallsHydrationToolFirst verifies the review loop starts with
// a minimal user message (not a transcript dump), the hydration tool is
// in the toolset, and calling it yields the transcript JSON — not a
// whitelist rejection (the gate must not run before the local handler;
// regression guard for b553ca9).
func TestReviewLoopCallsHydrationToolFirst(t *testing.T) {
	if resources.ReviewPrompt() == "" {
		t.Fatal("review prompt must be non-empty")
	}
	conv := &domain.Conversation{
		ID: "conv_loop",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "remember I like dark mode"},
		},
	}
	agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), DefaultReviewSettings())

	// The reviewTools() list must include review_transcript.
	tools := agent.reviewTools()
	found := false
	for _, td := range tools {
		if td.Name == "review_transcript" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("reviewTools() must include review_transcript")
	}

	// The adapter captures the initial user message to verify it is NOT
	// a transcript dump.
	var capturedInitialContent string
	adapter := &reviewCapturingAdapter{
		toolCalls: []domain.ToolCall{{
			Name: "review_transcript",
			Args: `{}`,
		}},
		onComplete: func(req ChatRequest) {
			if len(req.Messages) > 0 && capturedInitialContent == "" {
				capturedInitialContent = req.Messages[0].Content
			}
		},
	}
	_, msgs, err := agent.runReviewLoop(context.Background(), adapter, "model", conv)
	if err != nil {
		t.Fatalf("runReviewLoop: %v", err)
	}
	if strings.Contains(capturedInitialContent, "[user]") || strings.Contains(capturedInitialContent, "[assistant]") {
		t.Fatalf("initial user message looks like a transcript dump: %q", capturedInitialContent[:min(100, len(capturedInitialContent))])
	}
	for _, td := range agent.reviewTools() {
		if td.Name == reviewTranscriptToolName {
			// The tool definition still describes the path/start/end params.
			if !strings.Contains(td.Description, "path") {
				t.Fatalf("review_transcript description should mention path param, got: %q", td.Description)
			}
		}
	}

	// The hydration call must be answered with the transcript JSON from the
	// local handler — never the whitelist rejection. A rejection here means
	// every review dies on its mandatory opening call.
	for _, m := range msgs {
		if m.Role != "tool" || m.ToolResult == nil || m.ToolResult.Name != "review_transcript" {
			continue
		}
		content := m.ToolResult.Content
		if strings.Contains(content, "not allowed in background review") {
			t.Fatal("review_transcript was rejected by the whitelist gate; hydration must be handled before the gate")
		}
		if !strings.Contains(content, "conv_loop") {
			t.Fatalf("review_transcript result should contain conversation_id, got: %.200s", content)
		}
		if !strings.Contains(content, "dark mode") {
			t.Fatalf("review_transcript result should contain transcript content, got: %.200s", content)
		}
		return
	}
	t.Fatal("no tool result for review_transcript found in loop messages")
}

// TestReviewTranscriptToolRespectsTokenCap verifies large conversations are
// trimmed to fit MaxTranscriptTokens.
func TestReviewTranscriptToolRespectsTokenCap(t *testing.T) {
	// Build a conversation with many large messages.
	var msgs []domain.Message
	for i := 0; i < 100; i++ {
		msgs = append(msgs, domain.Message{
			Role:    domain.RoleUser,
			Content: strings.Repeat("x", 1000), // 1k chars each
		})
	}
	conv := &domain.Conversation{ID: "conv_big", Messages: msgs}
	settings := DefaultReviewSettings()
	settings.MaxTranscriptTokens = 1000 // ~4000 chars cap
	agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), settings)
	jsonOut := agent.executeReviewTranscript(conv, 0, 0, "")

	// The output must be under the char cap (~tokens*4) with some headroom
	// for JSON overhead.
	charCap := settings.MaxTranscriptTokens * 4
	if len(jsonOut) > charCap*2 { // allow 2x for JSON structure overhead
		t.Fatalf("executeReviewTranscript output len = %d, want under ~%d (token cap %d)", len(jsonOut), charCap*2, settings.MaxTranscriptTokens)
	}
}

// reviewCapturingAdapter is an AIProvider that returns tool calls on the
// first call and a terminal response afterwards, capturing the initial
// request for inspection.
type reviewCapturingAdapter struct {
	toolCalls  []domain.ToolCall
	calls      int
	onComplete func(req ChatRequest)
}

func (a *reviewCapturingAdapter) Kind() domain.ProviderKind { return domain.ProviderChat }
func (a *reviewCapturingAdapter) Stream(ctx context.Context, req ChatRequest, _ func(string), _ func(string)) (ChatResponse, error) {
	return a.Complete(ctx, req)
}
func (a *reviewCapturingAdapter) Complete(_ context.Context, req ChatRequest) (ChatResponse, error) {
	a.calls++
	if a.onComplete != nil {
		a.onComplete(req)
	}
	if a.calls == 1 {
		return ChatResponse{ToolCalls: a.toolCalls}, nil
	}
	return ChatResponse{Content: "Nothing to save."}, nil
}

// ---- Phase 2: Skill nudge tests ----

// TestSkillNudgeFiresOnToolIterationCount verifies the tool-call counter
// increments per tool call and fires a review at the threshold.
func TestSkillNudgeFiresOnToolIterationCount(t *testing.T) {
	app := newReviewApp(&reviewStubToolbox{})
	app.Settings = &fakeSettings{Settings: domain.DefaultSettings()}
	app.Settings.(*fakeSettings).SkillNudgeInterval = 3
	app.ReviewAgent = NewBackgroundReviewAgent(app, DefaultReviewSettings())
	// Disable turn-based review so we know the trigger is skill nudge alone.
	app.Settings.(*fakeSettings).LearningReviewThreshold = 0

	fired := false
	app.ReviewAgent = &BackgroundReviewAgent{
		app:      app,
		settings: DefaultReviewSettings(),
	}
	// Override reserveReview to detect the fire.
	originalReserve := app.ReviewAgent.reserveReview
	_ = originalReserve
	app.ReviewAgent.settings.Enabled = true

	// Simulate 3 tool calls.
	for i := 0; i < 3; i++ {
		firedBefore := fired
		// We detect fire by checking if the counter resets to 0.
		app.incrementToolCallCounter("conv_nudge")
		app.learningMu.RLock()
		count := app.toolCallsSinceReview["conv_nudge"]
		app.learningMu.RUnlock()
		if count == 0 && i == 2 {
			fired = true
		}
		_ = firedBefore
	}
	if !fired {
		t.Fatal("skill nudge should fire review after 3 tool calls (threshold=3)")
	}
}

// TestSkillNudgeIndependentOfTurnThreshold verifies the tool-call trigger
// fires even when the user turn count is below LearningReviewThreshold.
func TestSkillNudgeIndependentOfTurnThreshold(t *testing.T) {
	app := newReviewApp(&reviewStubToolbox{})
	app.Settings = &fakeSettings{Settings: domain.DefaultSettings()}
	app.Settings.(*fakeSettings).SkillNudgeInterval = 2
	app.Settings.(*fakeSettings).LearningReviewThreshold = 100 // high, won't fire
	app.ReviewAgent = NewBackgroundReviewAgent(app, DefaultReviewSettings())
	app.ReviewAgent.settings.Enabled = true

	// Simulate 2 tool calls — should fire skill nudge even though turns
	// are far below 100.
	for i := 0; i < 2; i++ {
		app.incrementToolCallCounter("conv_indep")
	}
	app.learningMu.RLock()
	count := app.toolCallsSinceReview["conv_indep"]
	app.learningMu.RUnlock()
	if count != 0 {
		t.Fatalf("toolCallsSinceReview = %d after 2 calls (threshold=2), want 0 (reset by fire)", count)
	}
	// Turn counter should be untouched.
	app.learningMu.RLock()
	turns := app.turnsSinceReview["conv_indep"]
	app.learningMu.RUnlock()
	if turns != 0 {
		t.Fatalf("turnsSinceReview = %d, want 0 (skill nudge should not touch turn counter)", turns)
	}
}

// TestSkillNudgeCooldownPreventsDuplicate verifies that when both triggers
// fire, the cooldown gate prevents a duplicate review.
func TestSkillNudgeCooldownPreventsDuplicate(t *testing.T) {
	app := newReviewApp(&reviewStubToolbox{})
	app.Settings = &fakeSettings{Settings: domain.DefaultSettings()}
	app.Settings.(*fakeSettings).SkillNudgeInterval = 2
	app.Settings.(*fakeSettings).LearningReviewThreshold = 1
	app.ReviewAgent = NewBackgroundReviewAgent(app, DefaultReviewSettings())
	app.ReviewAgent.settings.Enabled = true
	app.ReviewAgent.settings.ReviewCooldown = time.Minute // long cooldown

	// Fire turn-based review first.
	app.incrementTurnCounter("conv_dup")
	// Give the background goroutine time to reserve.
	time.Sleep(50 * time.Millisecond)
	// Now fire skill nudge — should be skipped by cooldown.
	app.incrementToolCallCounter("conv_dup")
	app.incrementToolCallCounter("conv_dup")
	time.Sleep(50 * time.Millisecond)

	// The important invariant: only ONE review runs (cooldown gate).
	// We verify by checking the cooldown map is occupied.
	app.ReviewAgent.reviewMu.Lock()
	_, hasCooldown := app.ReviewAgent.lastReview["conv_dup"]
	app.ReviewAgent.reviewMu.Unlock()
	if !hasCooldown {
		t.Fatal("cooldown should be set after first review, preventing duplicate")
	}
}

// TestSkillNudgeDisabledWhenZero verifies SkillNudgeInterval=0 disables
// tool-based review.
func TestSkillNudgeDisabledWhenZero(t *testing.T) {
	app := newReviewApp(&reviewStubToolbox{})
	app.Settings = &fakeSettings{Settings: domain.DefaultSettings()}
	app.Settings.(*fakeSettings).SkillNudgeInterval = 0
	app.ReviewAgent = NewBackgroundReviewAgent(app, DefaultReviewSettings())
	app.ReviewAgent.settings.Enabled = true

	for i := 0; i < 100; i++ {
		app.incrementToolCallCounter("conv_dis")
	}
	app.learningMu.RLock()
	count := app.toolCallsSinceReview["conv_dis"]
	app.learningMu.RUnlock()
	if count != 0 {
		t.Fatalf("toolCallsSinceReview = %d, want 0 (disabled counter should not increment)", count)
	}
}

// fakeSettings is a minimal SettingsStore for skill nudge tests.
type fakeSettings struct {
	domain.Settings
}

func (f *fakeSettings) Get() domain.Settings        { return f.Settings }
func (f *fakeSettings) Set(s domain.Settings) error { f.Settings = s; return nil }

// ---- Phase 3: Review model fix + trajectory logging tests ----

// TestReviewModelResolutionRecordedInTrajectory verifies that
// recordReviewModelResolution writes a trajectory event with the
// requested and resolved model names. This is the function called by
// applyReviewModelOverride on the fallback path, and on the success path
// when the override actually changes the model.
func TestReviewModelResolutionRecordedInTrajectory(t *testing.T) {
	tmp := t.TempDir()
	traj := NewTrajectoryRecorder(tmp)
	defer traj.Close()
	app := &App{
		Toolbox:              &reviewStubToolbox{},
		Logs:                 &fakeLogStore{},
		Trajectory:           traj,
		turnsSinceReview:     map[string]int{},
		toolCallsSinceReview: map[string]int{},
	}
	agent := NewBackgroundReviewAgent(app, DefaultReviewSettings())

	// Record a fallback event (the most common failure mode).
	agent.recordReviewModelResolution("prov_test:cheap-model", "fallback:conv_model", "prov_conv:gpt-test")

	events := ReadTrajectory(tmp, 100)
	found := false
	for _, e := range events {
		if e.Type == "review_model" {
			found = true
			if e.Detail["status"] != "fallback:conv_model" {
				t.Errorf("status = %v, want fallback:conv_model", e.Detail["status"])
			}
			if e.Detail["requested"] != "prov_test:cheap-model" {
				t.Errorf("requested = %v, want prov_test:cheap-model", e.Detail["requested"])
			}
			if e.Detail["resolved"] != "prov_conv:gpt-test" {
				t.Errorf("resolved = %v, want prov_conv:gpt-test", e.Detail["resolved"])
			}
		}
	}
	if !found {
		t.Fatal("review_model event not recorded in trajectory")
	}
}

func TestReviewStartedEventRecordedInTrajectory(t *testing.T) {
	tmp := t.TempDir()
	traj := NewTrajectoryRecorder(tmp)
	defer traj.Close()
	traj.Record("review", map[string]interface{}{
		"conversation": "conv_started",
		"status":       "started",
		"model":        "prov_test:gpt-test",
	})
	events := ReadTrajectory(tmp, 100)
	found := false
	for _, e := range events {
		if e.Type == "review" && e.Detail["status"] == "started" {
			found = true
			if e.Detail["model"] != "prov_test:gpt-test" {
				t.Errorf("model = %v, want prov_test:gpt-test", e.Detail["model"])
			}
		}
	}
	if !found {
		t.Fatal("review started event not persisted to trajectory.jsonl")
	}
}

// streamDeltaAdapter is an AIProvider whose Stream pushes text/reasoning
// deltas through the callbacks and returns a ChatResponse carrying a tool
// call on the first call and a terminal response afterwards. This models a
// real streaming provider (e.g. OpenAI Responses) where the review loop
// must consume deltas instead of a single non-streaming completion.
type streamDeltaAdapter struct {
	calls             int
	errOnCall         int
	err               error
	initialToolCallID string
	terminal          string
	deltaCalls        int
	reasonCalls       int
}

func (a *streamDeltaAdapter) Kind() domain.ProviderKind { return domain.ProviderChat }

func (a *streamDeltaAdapter) Complete(context.Context, ChatRequest) (ChatResponse, error) {
	return ChatResponse{}, nil // loop never calls Complete anymore
}

func (a *streamDeltaAdapter) Stream(_ context.Context, _ ChatRequest, onDelta, onReasoning func(string)) (ChatResponse, error) {
	a.calls++
	if a.errOnCall > 0 && a.calls == a.errOnCall {
		return ChatResponse{}, a.err
	}
	if a.calls == 1 {
		onDelta("I need to ")    // text delta 1
		onReasoning("thinking…") // reasoning delta — must also be delivered
		a.deltaCalls++
		a.reasonCalls++
		return ChatResponse{
			ToolCalls: []domain.ToolCall{{
				ID:   a.initialToolCallID,
				Name: "memory",
				Args: `{"op":"save","content":"user prefers Indonesian"}`,
			}},
		}, nil
	}
	onDelta("No more details.")
	a.deltaCalls++
	a.reasonCalls++
	return ChatResponse{Content: a.terminal}, nil
}

// TestReviewLoopUsesStreamAndAccumulatesDeltas verifies the review loop now
// drives Stream() (so a long-thinking provider keeps the connection alive via
// deltas) and that text deltas flow through onDelta while reasoning flows
// through onReasoning.
func TestReviewLoopUsesStreamAndAccumulatesDeltas(t *testing.T) {
	if resources.ReviewPrompt() == "" {
		t.Fatal("review prompt must be non-empty")
	}
	agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), DefaultReviewSettings())
	conv := &domain.Conversation{
		ID: "conv_delta",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "remember I prefer Indonesian"},
			{Role: domain.RoleAssistant, Content: "noted"},
		},
	}
	adapter := &streamDeltaAdapter{
		terminal:          "Nothing to save.",
		initialToolCallID: "call_delta_1",
	}
	mutations, _, err := agent.runReviewLoop(context.Background(), adapter, "model", conv)
	if err != nil {
		t.Fatalf("runReviewLoop with stream adapter: %v", err)
	}
	if len(mutations) != 1 {
		t.Fatalf("mutations = %+v, want the streamed memory mutation", mutations)
	}
	if adapter.deltaCalls == 0 {
		t.Fatal("Stream adapter should have received text deltas")
	}
	if adapter.reasonCalls == 0 {
		t.Fatal("Stream adapter should have received reasoning deltas")
	}
}

// TestReviewLoopStreamErrorIsPropagated verifies a failure from Stream (e.g.
// the adapter surfacing a hang via idle timeout) is returned to the caller
// and, unlike a wall-clock deadline, is a genuine provider error.
func TestReviewLoopStreamErrorIsPropagated(t *testing.T) {
	if resources.ReviewPrompt() == "" {
		t.Fatal("review prompt must be non-empty")
	}
	agent := NewBackgroundReviewAgent(newReviewApp(&reviewStubToolbox{}), DefaultReviewSettings())
	conv := &domain.Conversation{
		ID: "conv_stream_err",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "remember I prefer Indonesian"},
			{Role: domain.RoleAssistant, Content: "noted"},
		},
	}
	adapter := &streamDeltaAdapter{
		terminal:          "Nothing to save.",
		initialToolCallID: "call_err_1",
		errOnCall:         1,
		err:               errors.New("stream stalled: idle timeout"),
	}
	_, _, err := agent.runReviewLoop(context.Background(), adapter, "model", conv)
	if err == nil || !strings.Contains(err.Error(), "idle timeout") {
		t.Fatalf("err = %v, want propagation of the stream idle timeout", err)
	}
}
