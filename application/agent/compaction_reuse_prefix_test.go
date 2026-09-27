package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	appprovider "nusashell/application/provider"
	"nusashell/application/tools"
	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

type recordingCompactionAdapter struct {
	mu       sync.Mutex
	requests []*core.Request
}

func (a *recordingCompactionAdapter) Name() string { return "recording-compaction" }
func (a *recordingCompactionAdapter) Stream(context.Context, *core.Request) (core.Stream, error) {
	return nil, fmt.Errorf("stream not used")
}
func (a *recordingCompactionAdapter) Chat(_ context.Context, req *core.Request) (*core.Response, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, req)
	args, _ := json.Marshal(map[string]string{"text": compactionTestSummary})
	return &core.Response{
		Blocks:       []core.Block{core.ToolUseBlock{ID: "summary_0", Name: compactionSummaryToolName, Arguments: args}},
		FinishReason: core.FinishReasonToolCall,
	}, nil
}

type listingToolbox struct {
	infos []tools.ToolInfo
}

func (b *listingToolbox) ListTools() []tools.ToolInfo { return b.infos }
func (b *listingToolbox) Execute(_ context.Context, name string, _ []byte) (string, error) {
	return "ok:" + name, nil
}

func reuseCompactionFixture() (*domain.Conversation, domain.Settings) {
	body := strings.Repeat("conversation detail ", 400)
	msgs := []domain.Message{
		{ID: "u0", Role: domain.RoleUser, Content: "start the task", Status: domain.StatusDone},
		{ID: "hyd", Role: domain.RoleAssistant, Reasoning: "runtime snapshot", Status: domain.StatusDone,
			ToolCalls: []domain.ToolCall{{ID: domain.HydrateToolCallPrefix + "abc_0", Name: "runtime_context", Args: "{}", Output: `{"workspace":"/w"}`, Status: domain.ToolOK}}},
		{ID: "a0", Role: domain.RoleAssistant, Content: "reading the file", Status: domain.StatusDone,
			ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "file_read", Args: `{"path":"/w/a.go"}`, Output: body, Status: domain.ToolOK}}},
		{ID: "a1", Role: domain.RoleAssistant, Reasoning: "thinking without visible text", Status: domain.StatusDone},
	}
	for i := 0; i < 6; i++ {
		msgs = append(msgs,
			domain.Message{ID: fmt.Sprintf("u%d", i+1), Role: domain.RoleUser, Content: body, Status: domain.StatusDone},
			domain.Message{ID: fmt.Sprintf("r%d", i+1), Role: domain.RoleAssistant, Content: body, Status: domain.StatusDone},
		)
	}
	conv := &domain.Conversation{ID: "conv-reuse-prefix", Status: "running", Workspace: "/w", Messages: msgs}
	settings := domain.DefaultSettings()
	settings.CompactionWorkflow = domain.CompactionWorkflowReuse
	settings.CompactionSummaryMaxTokens = 800
	settings.PromptCaching = true
	return conv, settings
}

// TestReuseCompactionRequestSharesLiveTurnPrefix pins the reuse workflow's
// only reason to exist: the compaction request must be the live turn request
// for the archived prefix plus one handoff user message, so the provider can
// serve tools, system prompt, and history from the prompt cache.
func TestReuseCompactionRequestSharesLiveTurnPrefix(t *testing.T) {
	conv, settings := reuseCompactionFixture()
	store := &lifecycleConvStore{byID: map[string]*domain.Conversation{conv.ID: conv}}
	adapter := &recordingCompactionAdapter{}
	svc := New(Deps{
		Conversations: store,
		Toolbox: &listingToolbox{infos: []tools.ToolInfo{{
			Name: "file_read", Description: "read files", InputSchema: map[string]any{"type": "object"},
		}}},
		Settings: compactionSettings{s: settings},
		Bus:      noopEmitter{},
	})
	providerMeta := &domain.Provider{ID: "prov1", Kind: domain.ProviderChat, Driver: domain.ProviderDriverOpenRouter,
		BaseURL: "https://openrouter.ai/api/v1", Models: []domain.Model{{ID: "model", Context: 40000}}}
	pc := NewProviderContext(providerMeta, adapter)
	pc.Kind = domain.ProviderChat
	pc.OpenRouter = true
	caps := ModelCapabilities{ReasoningReplay: true}
	run := &TurnRun{ID: "run-reuse", ConversationID: conv.ID, Workspace: conv.Workspace, Ctx: context.Background()}

	toolDefs := svc.TurnToolDefs(run, settings)
	system := buildSystemPromptForRun(run, conv, settings.UserPrompt)
	cache := buildPromptCachePolicyForRequest(settings, providerMeta, "model", conv.ID, promptCachePrefixForRun(run), system, toolDefs)
	live := svc.buildTurnRequest(run, pc, cloneConversation(conv), "", "model", "high", toolDefs, settings, false, nil, 4000, cache, caps)
	liveCore := appprovider.ToCoreRequest(live, pc.Kind, pc.OpenRouter)

	turn := &compactionTurn{run: run, tools: toolDefs, effort: "high", promptCache: cache}
	if _, err := svc.runCompaction(context.Background(), run, conv, pc, "model", 40000, settings, caps, domain.CompactionTriggerProactive, turn); err != nil {
		t.Fatalf("runCompaction: %v", err)
	}

	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.requests) != 1 {
		t.Fatalf("compaction requests = %d, want 1 single-pass request", len(adapter.requests))
	}
	got := adapter.requests[0]

	if !reflect.DeepEqual(got.Tools, liveCore.Tools) {
		t.Fatalf("compaction tools differ from the live turn tools:\n got=%v\nlive=%v", toolNames(got.Tools), toolNames(liveCore.Tools))
	}
	if !containsTool(got.Tools, compactionSummaryToolName) {
		t.Fatalf("reuse toolbox %v lacks %s", toolNames(got.Tools), compactionSummaryToolName)
	}
	if gotKey, liveKey := got.ProviderOptions["prompt_cache_key"], liveCore.ProviderOptions["prompt_cache_key"]; gotKey != liveKey {
		t.Fatalf("prompt_cache_key = %v, want live key %v", gotKey, liveKey)
	}

	n := len(got.Messages) - 1
	if n < 3 || n >= len(liveCore.Messages) {
		t.Fatalf("compaction prefix has %d messages, live has %d; want a strict non-trivial prefix", n, len(liveCore.Messages))
	}
	for i := 0; i < n; i++ {
		if !reflect.DeepEqual(got.Messages[i], liveCore.Messages[i]) {
			t.Fatalf("message %d diverges from the live request:\n got=%+v\nlive=%+v", i, got.Messages[i], liveCore.Messages[i])
		}
	}
	last := got.Messages[n]
	if last.Role != core.RoleUser || !strings.Contains(coreMessageTextForTest(last), "Call the summary tool exactly once") {
		t.Fatalf("last compaction message = %+v, want the handoff user message", last)
	}

	var sawHydration bool
	for _, m := range got.Messages[:n] {
		for _, b := range m.Blocks {
			if tu, ok := b.(core.ToolUseBlock); ok && domain.IsHydrationCallID(tu.ID) {
				sawHydration = true
			}
		}
	}
	if !sawHydration {
		t.Fatal("compaction prefix dropped the hydration checkpoint that the live request carries")
	}
}

func TestTurnToolDefsAdvertiseSummaryOnlyForReuseWorkflow(t *testing.T) {
	svc := New(Deps{Toolbox: &listingToolbox{infos: []tools.ToolInfo{{
		Name: "file_read", Description: "read files", InputSchema: map[string]any{"type": "object"},
	}}}})
	run := &TurnRun{ID: "run", ConversationID: "c"}

	settings := domain.DefaultSettings()
	settings.CompactionWorkflow = domain.CompactionWorkflowDedicated
	if defs := svc.TurnToolDefs(run, settings); containsToolDef(defs, compactionSummaryToolName) {
		t.Fatalf("dedicated workflow advertised %s in the live toolbox", compactionSummaryToolName)
	}
	settings.CompactionWorkflow = domain.CompactionWorkflowReuse
	defs := svc.TurnToolDefs(run, settings)
	if !containsToolDef(defs, compactionSummaryToolName) || !containsToolDef(defs, "file_read") {
		t.Fatalf("reuse workflow toolbox = %v, want file_read and %s", defs, compactionSummaryToolName)
	}
}

func TestSummaryToolOutsideCompactionIsRejected(t *testing.T) {
	store := &lifecycleConvStore{byID: map[string]*domain.Conversation{"c": {ID: "c", Status: "running"}}}
	svc := New(Deps{Conversations: store, Toolbox: &listingToolbox{}, Settings: staticSettings{}, Bus: noopEmitter{}})
	run := &TurnRun{ID: "run", ConversationID: "c", Ctx: context.Background()}
	res := svc.RunOneTool(run, "msg", domain.ToolCall{ID: "call", Name: compactionSummaryToolName, Args: `{"text":"x"}`}, ModelCapabilities{}, domain.DefaultSettings(), 1)
	if res.Status != domain.ToolFailed || !strings.Contains(res.Output, "context checkpoint") {
		t.Fatalf("summary outside compaction = %+v, want a failed call explaining it is only for context checkpoints", res)
	}
}

func toolNames(defs []core.Tool) []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Name)
	}
	return out
}

func containsTool(defs []core.Tool, name string) bool {
	for _, d := range defs {
		if d.Name == name {
			return true
		}
	}
	return false
}

func containsToolDef(defs []ToolDef, name string) bool {
	for _, d := range defs {
		if d.Name == name {
			return true
		}
	}
	return false
}

func coreMessageTextForTest(m core.Message) string {
	var sb strings.Builder
	for _, b := range m.Blocks {
		if tb, ok := b.(core.TextBlock); ok {
			sb.WriteString(tb.Text)
		}
	}
	return sb.String()
}
