package domain

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// testHandover builds a minimal handover content for domain tests. Domain
// cannot import resources, so tests use the prefix directly. The application
// layer builds the real handover from compacted-continue.md.
func testHandover(summary string) string {
	return CompactionSummaryPrefix + "\n" + summary
}

func TestMessageEstimateTokensIgnoresUsageAndDoesNotDoubleCountSteps(t *testing.T) {
	content := strings.Repeat("a", 40) // 10 tokens
	msg := Message{
		Content:   content,
		Reasoning: "think", // ignored when steps are present
		Usage:     &Usage{InputTokens: 9000, OutputTokens: 50},
		ToolCalls: []ToolCall{{Name: "web_fetch", Args: `{"q":"x"}`, Output: "hit"}},
		Steps: []MessageStep{
			{Type: StepReasoning, Content: "think"},
			{Type: StepText, Content: content},
			{Type: StepToolCalls, ToolCalls: []ToolCall{{Name: "web_fetch", Args: `{"q":"x"}`, Output: "hit"}}},
		},
	}

	got := msg.EstimateTokens()
	want := Message{
		Steps: []MessageStep{
			{Type: StepReasoning, Content: "think"},
			{Type: StepText, Content: content},
			{Type: StepToolCalls, ToolCalls: []ToolCall{{Name: "web_fetch", Args: `{"q":"x"}`, Output: "hit"}}},
		},
	}
	if got != want.EstimateTokens() {
		t.Fatalf("EstimateTokens() = %d, want %d (usage and mirrored flat fields must not inflate the estimate)", got, want.EstimateTokens())
	}
	if got >= 9000 {
		t.Fatalf("EstimateTokens() = %d includes usage input tokens", got)
	}
}

func TestMessageEstimateTokensFallsBackToFlatFieldsWithoutSteps(t *testing.T) {
	msg := Message{Content: "hello world", Reasoning: "hmm", ToolCalls: []ToolCall{{Name: "todo", Args: "{}"}}}
	if msg.EstimateTokens() <= 0 {
		t.Fatal("expected a positive estimate from flat fields")
	}
	equivalent := Message{Steps: []MessageStep{
		{Type: StepReasoning, Content: "hmm"},
		{Type: StepText, Content: "hello world"},
		{Type: StepToolCalls, ToolCalls: []ToolCall{{Name: "todo", Args: "{}"}}},
	}}
	if msg.EstimateTokens() != equivalent.EstimateTokens() {
		t.Fatal("flat fields and equivalent steps should estimate the same")
	}
}

// TestMessageEstimateTokensImageNotCountedByBase64Size: image attachments
// must not be counted by their base64 data URL length. A 1MB image encodes
// to ~1.3MB base64, which at chars/4 would be ~330k tokens — far more than
// any provider actually charges (typically 765-1000 tokens for a 1024x1024
// image). The estimate must use a resolution-based heuristic instead.
func TestMessageEstimateTokensImageNotCountedByBase64Size(t *testing.T) {
	// Simulate a 1MB image as a base64 data URL (~1.3MB of base64 chars).
	bigBase64 := "data:image/png;base64," + strings.Repeat("A", 1_300_000)
	msg := Message{
		Content: "What is this?",
		Attachments: []Attachment{
			{Type: "image", Name: "big.png", MediaType: "image/png", DataURL: bigBase64},
		},
	}
	got := msg.EstimateTokens()
	// "What is this?" = 14 chars = ~4 tokens. Image should add a small
	// fixed cost (hundreds, not hundreds of thousands).
	if got > 5000 {
		t.Fatalf("EstimateTokens() = %d for one image — should be resolution-based (~hundreds), not base64-char-based (~330k)", got)
	}
	if got < 100 {
		t.Fatalf("EstimateTokens() = %d — image should contribute a non-trivial token estimate", got)
	}
}

func TestDefaultTitleDoesNotSplitUTF8Rune(t *testing.T) {
	c := &Conversation{Messages: []Message{{
		Role:    RoleUser,
		Content: strings.Repeat("語", 60),
	}}}
	title := c.DefaultTitle()
	if !utf8.ValidString(title) {
		t.Fatalf("title is invalid UTF-8: %q", title)
	}
	if !strings.HasSuffix(title, "…") {
		t.Fatalf("title = %q, want truncated with ellipsis", title)
	}
}

func TestDefaultTitleSkipsBlankMessagesAndNormalizesWhitespace(t *testing.T) {
	c := &Conversation{Messages: []Message{
		{Role: RoleUser, Content: " \n\t "},
		{Role: RoleUser, Content: "Fix   the\nroom history"},
	}}
	if got := c.DefaultTitle(); got != "Fix the room history" {
		t.Fatalf("DefaultTitle() = %q, want normalized first non-empty user message", got)
	}
}

// TestCompactCountsToolCallsInTokenBudget: Compact must count tool call
// tokens when deciding which messages to retain. A conversation full of
// tool-only messages (empty content, non-trivial tool calls) must not
// be treated as "fits in budget" — that would skip compaction entirely
// and cause context overflow at the provider.
func TestCompactCountsToolCallsInTokenBudget(t *testing.T) {
	// 100 assistant messages, each with empty content but a tool call
	// with a large output. Total tool-call tokens >> keep budget.
	msgs := make([]Message, 100)
	for i := range msgs {
		msgs[i] = Message{
			ID:      NewID("msg"),
			Role:    RoleAssistant,
			Content: "",
			ToolCalls: []ToolCall{{
				ID:     NewID("call"),
				Name:   "mcp_enable",
				Args:   `{"id":"nusashell.terminal"}`,
				Output: strings.Repeat("x", 400), // ~100 tokens per call
			}},
		}
	}
	c := &Conversation{Messages: msgs}
	keepBudget := 1000 // small budget: should only retain ~10 messages
	archived := c.ArchiveMessages(keepBudget)
	if len(archived) == 0 {
		t.Fatal("ArchiveMessages returned nil — tool call tokens not counted, everything 'fits'")
	}
	if len(archived) < 80 {
		t.Fatalf("ArchiveMessages archived only %d messages, expected most to be archived (tool tokens undercounted)", len(archived))
	}
	c.Compact("summary", testHandover("summary"), keepBudget)
	if len(c.Messages) > 15 {
		t.Fatalf("after Compact, %d messages retained — tool call tokens not counted in budget", len(c.Messages))
	}
}

// TestCompactSummaryIsUserRole: the compaction summary must carry role=user
// so it appears in the provider request's messages array. Providers return
// HTTP 400 "No user query found in messages" without a user message. The
// summary must be distinguishable from a real user message via
// CompactionSummaryPrefix.
func TestCompactSummaryIsUserRole(t *testing.T) {
	c := &Conversation{
		Messages: []Message{
			{ID: "u1", Role: RoleUser, Content: "fix the bug"},
			{ID: "a1", Role: RoleAssistant, Content: "working on it"},
		},
	}
	c.Compact("summary of work", testHandover("summary of work"), 1000)

	first := c.Messages[0]
	if first.Role != RoleUser {
		t.Fatalf("compaction summary role = %s, want user", first.Role)
	}
	if !IsCompactionSummary(first.Content) {
		t.Fatalf("compaction summary content = %q, want prefix %q", first.Content, CompactionSummaryPrefix)
	}
}

// TestCompactPreservesChronologicalOrderAndPutsSummaryFirst: Compact must
// not regroup users then assistants (that scrambled the live transcript and
// parked the handover after the in-flight turn). Retained messages stay in
// original order; the summary is the first live message so the UI and the
// next provider request both see a user handoff followed by the recent turn.
func TestCompactPreservesChronologicalOrderAndPutsSummaryFirst(t *testing.T) {
	c := &Conversation{
		Messages: []Message{
			{ID: "u1", Role: RoleUser, Content: "first question"},
			{ID: "a1", Role: RoleAssistant, Content: "first answer"},
			{ID: "u2", Role: RoleUser, Content: "follow up"},
			{ID: "a2", Role: RoleAssistant, Content: "still working"},
		},
	}
	c.Compact("handoff of the work so far", testHandover("handoff of the work so far"), 10000)

	if len(c.Messages) < 5 {
		t.Fatalf("messages = %d, want summary + 4 retained", len(c.Messages))
	}
	if !IsCompactionSummary(c.Messages[0].Content) {
		t.Fatalf("first message is not the compaction summary: %q", c.Messages[0].Content)
	}
	got := make([]string, 0, 4)
	for _, m := range c.Messages[1:] {
		got = append(got, m.ID)
	}
	want := []string{"u1", "a1", "u2", "a2"}
	if len(got) != len(want) {
		t.Fatalf("retained ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("retained ids = %v, want chronological %v", got, want)
		}
	}
}

func TestCompactDropsPrefixInsteadOfPullingAllUsers(t *testing.T) {
	// Old users must be summarized, not pulled forward in front of recent
	// assistant turns.
	msgs := []Message{
		{ID: "u-old", Role: RoleUser, Content: strings.Repeat("old-user-", 80)},
		{ID: "a-old", Role: RoleAssistant, Content: strings.Repeat("old-asst-", 80)},
		{ID: "u-new", Role: RoleUser, Content: "latest question"},
		{ID: "a-new", Role: RoleAssistant, Content: "latest answer"},
	}
	c := &Conversation{Messages: msgs}
	c.Compact("summary", testHandover("summary"), 50) // tiny keep: only the latest turn fits

	ids := make([]string, 0, len(c.Messages))
	for _, m := range c.Messages {
		if IsCompactionSummary(m.Content) {
			continue
		}
		ids = append(ids, m.ID)
	}
	for i := 1; i < len(ids); i++ {
		// suffix clone: original relative order, no u-old sitting above a-new
		// without a-old.
		if ids[i] == "a-new" && containsID(ids[:i], "u-old") && !containsID(ids[:i], "a-old") {
			t.Fatalf("users piled above recent assistant: %v", ids)
		}
	}
	if containsID(ids, "u-new") && containsID(ids, "a-new") {
		ui, ai := indexOfID(ids, "u-new"), indexOfID(ids, "a-new")
		if ui > ai {
			t.Fatalf("latest turn reversed: %v", ids)
		}
	}
}

func containsID(ids []string, id string) bool {
	return indexOfID(ids, id) >= 0
}

func indexOfID(ids []string, id string) int {
	for i, v := range ids {
		if v == id {
			return i
		}
	}
	return -1
}
func TestCompactSummaryIsTheUserWhenSuffixHasNoUser(t *testing.T) {
	// Keep is a suffix clone. A long tool-only tail may drop the original
	// user message into the summary; the prepended handover still gives
	// the provider a user turn (no 400 "No user query found").
	msgs := make([]Message, 0, 102)
	msgs = append(msgs, Message{
		ID:      "u0",
		Role:    RoleUser,
		Content: "Please fix the bug in auth.go",
	})
	for i := 0; i < 100; i++ {
		msgs = append(msgs, Message{
			ID:      NewID("msg"),
			Role:    RoleAssistant,
			Content: "",
			ToolCalls: []ToolCall{{
				ID:     NewID("call"),
				Name:   "mcp_call",
				Args:   `{"ref":"nusashell.files:read"}`,
				Output: strings.Repeat("x", 400),
			}},
		})
	}
	c := &Conversation{Messages: msgs}
	c.Compact("summary of work done", testHandover("summary of work done"), 1000)

	if !IsCompactionSummary(c.Messages[0].Content) || c.Messages[0].Role != RoleUser {
		t.Fatal("Compact must prepend a user-role handover so the provider sees a user turn")
	}
	for _, m := range c.Messages[1:] {
		if m.ID == "u0" {
			t.Fatal("prefix user must not be pulled forward in front of the keep suffix")
		}
	}
}

// TestCompactExcludesPriorSummaryFromKeepSuffix: when compacting a
// conversation that already has a compaction summary, the prior summary
// is replaced — it must not leak into the cloned keep suffix.
func TestCompactExcludesPriorSummaryFromKeepSuffix(t *testing.T) {
	c := &Conversation{
		Messages: []Message{
			{ID: "s1", Role: RoleUser, Content: CompactionSummaryPrefix + "\nprior summary"},
			{ID: "u1", Role: RoleUser, Content: "real user message"},
			{ID: "a1", Role: RoleAssistant, Content: "response"},
		},
	}
	c.Compact("new summary", testHandover("new summary"), 1000)

	// The prior summary must not appear in the retained user messages.
	for _, m := range c.Messages {
		if m.Role == RoleUser && IsCompactionSummary(m.Content) && m.Content != CompactionSummaryPrefix+"\n"+c.Summary {
			t.Fatalf("prior compaction summary leaked into retained user messages: %q", m.Content)
		}
	}
}

// TestArchiveMessagesAndCompactAreConsistent: ArchiveMessages must return
// exactly the messages that Compact drops — no more, no less. If they
// disagree, archived chunks will either duplicate retained messages
// (wasting scroll-back space) or drop messages that should be archived
// (losing scroll-back history). This test verifies the invariant by
// archiving, compacting, and checking that archived and live messages
// partition the original set with no overlap and no loss.
func TestArchiveMessagesAndCompactAreConsistent(t *testing.T) {
	// 1 user message at the start, then 50 assistant+tool messages.
	// keepBudget is small so the prefix (including the original user) is
	// archived and the live suffix is a contiguous clone.
	msgs := make([]Message, 0, 51)
	msgs = append(msgs, Message{ID: "u0", Role: RoleUser, Content: "fix the bug"})
	for i := 0; i < 50; i++ {
		msgs = append(msgs, Message{
			ID:      NewID("msg"),
			Role:    RoleAssistant,
			Content: "",
			ToolCalls: []ToolCall{{
				ID:     NewID("call"),
				Name:   "mcp_call",
				Args:   `{}`,
				Output: strings.Repeat("x", 400),
			}},
		})
	}
	c := &Conversation{Messages: msgs}

	archived := c.ArchiveMessages(1000)
	c.Compact("summary", testHandover("summary"), 1000)

	// Build a set of live message IDs (excluding the new summary).
	liveIDs := make(map[string]bool)
	for _, m := range c.Messages {
		if IsCompactionSummary(m.Content) {
			continue // summary is new, not from original set
		}
		liveIDs[m.ID] = true
	}

	// No archived message may appear in live.
	for _, m := range archived {
		if liveIDs[m.ID] {
			t.Fatalf("message %s appears in both archive and live — ArchiveMessages and Compact disagree", m.ID)
		}
	}

	// Every original message must be either archived or live (no loss).
	archivedIDs := make(map[string]bool)
	for _, m := range archived {
		archivedIDs[m.ID] = true
	}
	for _, orig := range msgs {
		if !archivedIDs[orig.ID] && !liveIDs[orig.ID] {
			t.Fatalf("message %s lost — neither archived nor live", orig.ID)
		}
	}
}

func TestRecoverAbandonedTurnMarksInFlightAssistant(t *testing.T) {
	c := &Conversation{
		ID:     "conv_zombie",
		Status: "running",
		Messages: []Message{
			{ID: "u1", Role: RoleUser, Content: "hello", Status: StatusDone},
			{ID: "a1", Role: RoleAssistant, Content: "partial", ToolCalls: []ToolCall{
				{ID: "t1", Name: "exec", Status: ToolRunning},
			}},
			{ID: "a0", Role: RoleAssistant, Content: "earlier", Status: StatusDone},
		},
	}
	if !c.RecoverAbandonedTurn() {
		t.Fatal("expected recovery of running conversation")
	}
	if c.Status != "idle" {
		t.Fatalf("status = %q, want idle", c.Status)
	}
	if c.Messages[1].Status != StatusInterrupted {
		t.Fatalf("in-flight assistant status = %q, want interrupted", c.Messages[1].Status)
	}
	if c.Messages[1].Error != AbandonedTurnError {
		t.Fatalf("error = %q", c.Messages[1].Error)
	}
	if c.Messages[1].ToolCalls[0].Status != ToolInterrupted {
		t.Fatalf("tool status = %q, want interrupted", c.Messages[1].ToolCalls[0].Status)
	}
	if c.Messages[2].Status != StatusDone {
		t.Fatalf("completed assistant should stay done, got %q", c.Messages[2].Status)
	}
	if c.RecoverAbandonedTurn() {
		t.Fatal("idle conversation should not recover again")
	}
}

func TestRecoverOrphanedTurnUsesCustomReason(t *testing.T) {
	c := &Conversation{
		ID:     "conv_orphan",
		Status: "running",
		Messages: []Message{
			{ID: "u1", Role: RoleUser, Content: "hello", Status: StatusDone},
			{ID: "a1", Role: RoleAssistant, Content: "", Status: ""},
			{ID: "a0", Role: RoleAssistant, Content: "earlier", Status: StatusDone},
		},
	}
	if !c.RecoverOrphanedTurn("turn interrupted: agent panic") {
		t.Fatal("expected recovery of orphaned running conversation")
	}
	if c.Status != "idle" {
		t.Fatalf("status = %q, want idle", c.Status)
	}
	if c.Messages[1].Status != StatusInterrupted {
		t.Fatalf("in-flight assistant status = %q, want interrupted", c.Messages[1].Status)
	}
	if c.Messages[1].Error != "turn interrupted: agent panic" {
		t.Fatalf("error = %q, want custom reason", c.Messages[1].Error)
	}
	if c.Messages[2].Status != StatusDone {
		t.Fatalf("completed assistant should stay done, got %q", c.Messages[2].Status)
	}
}

func TestRecoverOrphanedTurnDefaultsReasonWhenEmpty(t *testing.T) {
	c := &Conversation{
		ID:     "conv_orphan2",
		Status: "running",
		Messages: []Message{
			{ID: "a1", Role: RoleAssistant, Content: "", Status: ""},
		},
	}
	if !c.RecoverOrphanedTurn("") {
		t.Fatal("expected recovery")
	}
	if c.Messages[0].Error != OrphanedTurnError {
		t.Fatalf("error = %q, want default OrphanedTurnError", c.Messages[0].Error)
	}
}
