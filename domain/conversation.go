package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

type MessageRole string

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleSystem    MessageRole = "system"
)

type MessageStatus string

const (
	StatusDone        MessageStatus = "done"
	StatusError       MessageStatus = "error"
	StatusInterrupted MessageStatus = "interrupted by user"
)

type ToolCallStatus string

const (
	ToolRunning     ToolCallStatus = "running"
	ToolOK          ToolCallStatus = "ok"
	ToolFailed      ToolCallStatus = "fail"
	ToolInterrupted ToolCallStatus = "interrupted by user"
)

type Usage struct {
	InputTokens  int
	OutputTokens int
	CacheRead    int
	CacheWrite   int
}

// Attachment is a durable user-provided message part. Text attachments retain
// their decoded content; image and PDF attachments retain their original data
// URL so compatible providers can receive the exact source bytes. Image and
// file attachments are also saved to disk under <datadir>/attachments/ and
// the absolute path is stored in FilePath so file-based tools can access them.
type Attachment struct {
	Type      string // text | image | file
	Name      string
	MediaType string
	Content   string // text only
	DataURL   string // image and file only
	FilePath  string // absolute path on disk for image/file attachments; empty for text
}

type ToolCall struct {
	ID     string
	Name   string
	Args   string // raw JSON, as sent by the model
	Status ToolCallStatus
	Output string
	// OutputAttachments carries image attachments returned by tools like
	// read_media and generate_image so vision-capable models can see them
	// in the tool result. Empty for all other tools. Persisted with the
	// tool call so subsequent rounds and conversation reloads keep the
	// images visible. Generated images persist FilePath as the source of
	// truth and omit DataURL to keep conversation JSON small.
	OutputAttachments []Attachment
	// Opaque stores provider-specific data that must be echoed back in
	// subsequent requests but has no universal meaning across providers.
	// Example: Gemini's thoughtSignature for thinking models — required
	// in functionCall parts of the next turn or the API returns 400.
	// Persisted with the conversation. nil for providers that don't use it.
	Opaque map[string]any `json:"opaque,omitempty"`
}

// StepType identifies a temporal segment within a multi-round assistant turn.
type StepType string

const (
	StepReasoning StepType = "reasoning"
	StepText      StepType = "text"
	StepToolCalls StepType = "tool_calls"
)

// MessageStep captures one temporal segment of an assistant turn: a reasoning
// block, a text block, or a group of tool calls. Steps are appended in
// chronological order so the UI can render the interleaved flow faithfully
// (thinking → tool → thinking → tool → reason).
type MessageStep struct {
	Type      StepType
	Content   string     // reasoning or text
	ToolCalls []ToolCall // for tool_calls steps
}

type Message struct {
	ID             string
	Role           MessageRole
	Content        string // final text (mirrors last text step, for backward compat)
	Reasoning      string // final reasoning (mirrors last reasoning step, for backward compat)
	Steps          []MessageStep
	Model          string
	ProviderID     string // provider that served this turn; "" for legacy messages
	Usage          *Usage
	CreatedAt      time.Time
	Status         MessageStatus
	Error          string
	ToolCalls      []ToolCall // all tool calls (mirrors tool_calls steps, for backward compat)
	Attachments    []Attachment
	Steer          bool // true for user messages injected mid-turn (steering)
	AutoContinue   bool // true for synthetic user messages injected by the auto-continue chain
	ContextUpdated bool // true when a fresh hydration checkpoint was persisted for this turn
}

type Conversation struct {
	ID         string
	Title      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Model      string
	Effort     string // reasoning effort: "auto" (omit) or a level from the model's SupportedEfforts
	Status     string // idle | running
	Summary    string // compaction summary, "" when never compacted
	Workspace  string // optional absolute working directory selected for this conversation
	Messages   []Message
	ChunkCount int // number of archived pre-compaction chunks available for scroll-back
	// CompactionBlob holds an opaque server-side compaction payload (e.g.
	// Codex encrypted_content) that only the originating provider can read.
	// When non-empty, the Codex adapter passes it back as a Compaction item
	// in subsequent request input. Empty for providers that don't support
	// server-side compaction.
	CompactionBlob string
	// EstimatedTokens is the last server-side *heuristic* context estimate for
	// this conversation (system + messages + tool definitions, ~chars/4). It
	// is a provisional live number shown while a turn streams, before the
	// provider reports real usage; it is a fallback for ContextTokens.
	EstimatedTokens int64
	// ContextTokens is the authoritative provider-measured context fill after
	// the last completed turn (last round's input + cached input + output).
	// This is the source of truth for the idle context badge. Zero until a
	// turn completes with provider usage (e.g. some local providers report
	// none), in which case the UI falls back to EstimatedTokens.
	ContextTokens int64
	// LastReviewedMsgCount is the total message count at the time of the
	// most recent background learning review. The review agent uses this as
	// an incremental marker: each review only processes messages from
	// LastReviewedMsgCount onward, avoiding re-reading already reviewed
	// content. Zero means "never reviewed" (review from the start).
	LastReviewedMsgCount int `json:"last_reviewed_msg_count,omitempty"`
}

// NewConversation creates an empty conversation.
func NewConversation(id, title string) *Conversation {
	now := time.Now().UTC()
	return &Conversation{
		ID:        id,
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
		Status:    "idle",
	}
}

func (c *Conversation) Touch() { c.UpdatedAt = time.Now().UTC() }

// AbandonedTurnError is stored on in-flight assistant messages when a process
// restart finds a conversation still marked running. No live turn can exist
// after load, so the conversation is returned to idle.
const AbandonedTurnError = "turn interrupted: server restarted while the turn was running"

// RecoverAbandonedTurn converts a crash-leftover Status=="running" conversation
// into idle and marks unfinished assistant work as interrupted. Returns true
// when the conversation was mutated and should be persisted.
func (c *Conversation) RecoverAbandonedTurn() bool {
	return c.recoverRunningTurn(AbandonedTurnError)
}

// OrphanedTurnError is stored on in-flight assistant messages when a turn
// exits without a terminal state (e.g. panic recovered by goSafe) while the
// process is still running. The conversation is returned to idle so the user
// is not permanently blocked.
const OrphanedTurnError = "turn interrupted: agent exited unexpectedly without completing"

// RecoverOrphanedTurn is like RecoverAbandonedTurn but uses a custom reason
// for the error message. Used when a turn exits without a terminal state
// (panic, early return) while the process is still running, so the
// conversation is not left permanently stuck in "running".
func (c *Conversation) RecoverOrphanedTurn(reason string) bool {
	if reason == "" {
		reason = OrphanedTurnError
	}
	return c.recoverRunningTurn(reason)
}

func (c *Conversation) recoverRunningTurn(reason string) bool {
	if c == nil || c.Status != "running" {
		return false
	}
	c.Status = "idle"
	for i := range c.Messages {
		m := &c.Messages[i]
		if m.Role != RoleAssistant {
			continue
		}
		interruptedTools := interruptAbandonedTools(m)
		if m.Status == "" || interruptedTools {
			m.Status = StatusInterrupted
			if m.Error == "" {
				m.Error = reason
			}
		}
	}
	c.Touch()
	return true
}

func interruptAbandonedTools(m *Message) bool {
	changed := false
	for i := range m.ToolCalls {
		if abandonTool(&m.ToolCalls[i]) {
			changed = true
		}
	}
	for i := range m.Steps {
		for j := range m.Steps[i].ToolCalls {
			if abandonTool(&m.Steps[i].ToolCalls[j]) {
				changed = true
			}
		}
	}
	return changed
}

func abandonTool(tc *ToolCall) bool {
	if tc.Status != ToolRunning && tc.Status != "" {
		return false
	}
	tc.Status = ToolInterrupted
	if tc.Output == "" {
		tc.Output = "interrupted by user"
	}
	return true
}

// DefaultTitle derives a title from the first user message.
func (c *Conversation) DefaultTitle() string {
	for _, m := range c.Messages {
		if m.Role == RoleUser {
			return truncateRunes(m.Content, 48)
		}
	}
	return "Untitled"
}

// AddMessage appends and touches the conversation.
func (c *Conversation) AddMessage(m Message) {
	c.Messages = append(c.Messages, m)
	c.Touch()
}

// EstimateTokens sums the message content, tool args and outputs.
func (c *Conversation) EstimateTokens() int {
	total := 0
	for _, m := range c.Messages {
		total += m.EstimateTokens()
	}
	if c.Summary != "" {
		total += EstimateTokens(c.Summary)
	}
	return total
}

// EstimateTokens estimates the token cost of a single message. Provider usage
// is not part of the size estimate: those values describe the last request,
// not the stored message. When Steps are present they are the chronological
// source of truth and the mirrored Content/Reasoning/ToolCalls fields are
// ignored so multi-round turns are not double-counted.
func (m Message) EstimateTokens() int {
	total := 0
	if len(m.Steps) > 0 {
		for _, s := range m.Steps {
			total += EstimateTokens(s.Content)
			for _, tc := range s.ToolCalls {
				total += estimateToolCallTokens(tc)
			}
		}
	} else {
		total += EstimateTokens(m.Content)
		total += EstimateTokens(m.Reasoning)
		for _, tc := range m.ToolCalls {
			total += estimateToolCallTokens(tc)
		}
	}
	for _, attachment := range m.Attachments {
		total += EstimateTokens(attachment.Name)
		total += EstimateTokens(attachment.Content)
		if attachment.Type == "image" {
			total += estimateImageTokens(attachment.DataURL)
		} else {
			// Non-image file attachments: count the data URL as text.
			// PDFs and other documents are usually sent as descriptive
			// markers, not raw bytes, so this is a small contribution.
			total += EstimateTokens(attachment.DataURL)
		}
	}
	return total
}

// estimateImageTokens estimates the token cost of an image attachment using
// a resolution-based heuristic, matching how providers (OpenAI, Anthropic)
// actually charge for images: by tile count, not by byte size.
//
// Base64 encoding inflates binary data by ~33%, and EstimateTokens (chars/4)
// on a base64 string would count a 1MB image as ~330k tokens — far more than
// any provider charges (a 1024x1024 image is ~765 tokens on OpenAI).
//
// We can't decode the image here (domain must stay pure), so we estimate the
// decoded byte size from the base64 length and use a conservative heuristic:
// ~1 token per 256 decoded bytes, capped at 2000 tokens for very large images.
// This is still a rough estimate but is within an order of magnitude of the
// real provider cost, unlike the base64-char-based estimate which was off by
// ~400x.
func estimateImageTokens(dataURL string) int {
	if dataURL == "" {
		return 0
	}
	// Strip the "data:...;base64," prefix to get the base64 payload.
	_, b64, ok := strings.Cut(dataURL, ",")
	if !ok {
		return 0
	}
	// Base64 encodes 3 bytes as 4 chars, so decoded size ≈ len*3/4.
	decodedBytes := len(b64) * 3 / 4
	// Heuristic: ~1 token per 256 decoded bytes. A 1MB image → ~4000 tokens.
	// Cap at 2000 to avoid inflating context for very large images; providers
	// typically downscale before tokenizing anyway.
	tokens := decodedBytes / 256
	if tokens > 2000 {
		tokens = 2000
	}
	if tokens < 85 {
		// Minimum token cost for any image (OpenAI charges ~85 for a
		// 512x512 low-detail image).
		tokens = 85
	}
	return tokens
}

func estimateToolCallTokens(tc ToolCall) int {
	return EstimateTokens(tc.Name) + EstimateTokens(tc.Args) + EstimateTokens(tc.Output)
}

func truncateRunes(s string, n int) string {
	if n <= 0 || s == "" {
		return s
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n]) + "…"
}

// CompactionSummaryPrefix is the prefix that identifies a compaction summary
// message. It lets chatMessages, buildSystemPrompt, and the UI distinguish a
// compaction handover from a real user message even though both carry
// role=user — matching the codex-rs design where the summary is the last
// user-visible item in the compacted history.
const CompactionSummaryPrefix = "Compacted context handover:"

// IsCompactionSummary reports whether a message content is a compaction
// summary marker. Used to skip compaction summaries when collecting real
// user messages for retention, and by the UI to render the marker.
func IsCompactionSummary(content string) bool {
	return strings.HasPrefix(content, CompactionSummaryPrefix)
}

// compactedUserMessageBudget is the token budget for retaining real user
// messages across compaction. User messages are the most important context
// (they carry the user's goal and constraints) and providers require at
// least one user message in the request, so they get a dedicated budget
// separate from the recent-message keep budget. Matches codex-rs
// COMPACT_USER_MESSAGE_MAX_TOKENS.
const compactedUserMessageBudget = 20000

// compactionRetention computes which message indices Compact would retain
// for the given keepTokenBudget and returns the stripped+truncated retained
// messages split by kind (user vs recent). Used by both Compact (to build
// the new message slice) and ArchiveMessages (to compute what gets dropped)
// so the two stay consistent — if they drift, archived chunks will either
// duplicate retained messages or drop messages that should be archived.
//
// Retention policy (matches codex-rs build_compacted_history):
//  1. Real user messages (excluding prior compaction summaries) within a
//     dedicated 20k-token budget, scanned backward from the most recent.
//  2. Recent non-user messages (assistant, tool calls) within keepTokenBudget,
//     scanned backward from the most recent.
//
// The compaction summary itself is not part of retention — Compact appends it
// after retention, and ArchiveMessages is called before Compact so the
// summary does not exist yet.
func (c *Conversation) compactionRetention(keepTokenBudget int) (retainedUserMsgs, retainedRecent []Message, retainedIndices map[int]bool) {
	retainedIndices = make(map[int]bool)

	// 1. User messages within dedicated budget (backward scan).
	remainingUser := compactedUserMessageBudget
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if remainingUser <= 0 {
			break
		}
		m := c.Messages[i]
		if m.Role != RoleUser || IsCompactionSummary(m.Content) {
			continue
		}
		stripped := StripForRetention(m)
		tokens := m.EstimateTokens()
		if tokens <= remainingUser {
			retainedUserMsgs = append([]Message{stripped}, retainedUserMsgs...)
			retainedIndices[i] = true
			remainingUser -= tokens
		} else {
			stripped = truncateMessageContent(stripped, remainingUser)
			if stripped.Content != "" || len(stripped.Attachments) > 0 {
				retainedUserMsgs = append([]Message{stripped}, retainedUserMsgs...)
				retainedIndices[i] = true
			}
			remainingUser = 0
		}
	}

	// 2. Non-user messages within keepTokenBudget (backward scan).
	remaining := keepTokenBudget
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if remaining <= 0 {
			break
		}
		m := c.Messages[i]
		if m.Role == RoleUser {
			continue
		}
		stripped := StripForRetention(m)
		tokens := m.EstimateTokens()
		if tokens <= remaining {
			retainedRecent = append([]Message{stripped}, retainedRecent...)
			retainedIndices[i] = true
			remaining -= tokens
		} else {
			stripped = truncateMessageContent(stripped, remaining)
			if stripped.Content != "" || len(stripped.Attachments) > 0 {
				retainedRecent = append([]Message{stripped}, retainedRecent...)
				retainedIndices[i] = true
			}
			remaining = 0
		}
	}
	return retainedUserMsgs, retainedRecent, retainedIndices
}

// Compact replaces the oldest messages with a compaction summary and keeps:
//  1. Real user messages (excluding prior compaction summaries) within a
//     dedicated 20k-token budget, scanned backward from the most recent.
//  2. Recent non-user messages (assistant, tool calls) within keepTokenBudget,
//     scanned backward from the most recent.
//  3. The compaction summary as the final user message.
//
// The summary carries role=user so it appears in the provider request's
// messages array — providers require at least one user message and return
// HTTP 400 "No user query found in messages" without one. The
// CompactionSummaryPrefix lets the UI and buildSystemPrompt distinguish it
// from a real user message. This follows the codex-rs compaction design
// (build_compacted_history in core/src/compact.rs) where the summary is the
// last user-visible item in the compacted history.
func (c *Conversation) Compact(summary string, keepTokenBudget int) {
	if c.Summary != "" {
		c.Summary += "\n\n" + summary
	} else {
		c.Summary = summary
	}

	retainedUserMsgs, retainedRecent, _ := c.compactionRetention(keepTokenBudget)

	// Build the compaction summary as the final user message.
	summaryMsg := Message{
		ID:        NewID("msg"),
		Role:      RoleUser,
		Content:   CompactionSummaryPrefix + "\n" + c.Summary,
		CreatedAt: time.Now().UTC(),
		Status:    StatusDone,
	}

	c.Messages = append(append(retainedUserMsgs, retainedRecent...), summaryMsg)
	c.Touch()
}

// ArchiveMessages returns the messages that would be dropped by a compaction
// with the given keep-token budget. The returned slice preserves full message
// content (tool calls, reasoning, steps) so it can be archived for later
// scroll-back retrieval. Call this before Compact to capture what will be
// dropped. Returns nil if nothing would be archived (everything fits).
//
// The retention policy must match Compact exactly — if ArchiveMessages and
// Compact disagree on what is retained, archived chunks will either
// duplicate retained messages (wasting scroll-back space) or drop messages
// that should be archived (losing scroll-back history). Both use
// compactionRetention to stay in sync.
func (c *Conversation) ArchiveMessages(keepTokenBudget int) []Message {
	_, _, retainedIndices := c.compactionRetention(keepTokenBudget)
	if len(retainedIndices) >= len(c.Messages) {
		return nil
	}
	var archived []Message
	for i, m := range c.Messages {
		if !retainedIndices[i] {
			archived = append(archived, m)
		}
	}
	return archived
}

// StripForRetention returns a copy of the message with tool calls, reasoning,
// and steps removed. These are already captured in the compaction summary, so
// retaining them would duplicate context and waste the token budget.
func StripForRetention(m Message) Message {
	return Message{
		ID:          m.ID,
		Role:        m.Role,
		Content:     m.Content,
		Attachments: m.Attachments,
		CreatedAt:   m.CreatedAt,
		Status:      m.Status,
		Model:       m.Model,
	}
}

// truncateMessageContent truncates the message content string to fit within
// the given token budget. Since EstimateTokens is len/4, we keep approximately
// tokens*4 characters. Attachments are preserved as-is.
func truncateMessageContent(m Message, tokens int) Message {
	if tokens <= 0 {
		m.Content = ""
		return m
	}
	maxBytes := tokens * 4
	if len(m.Content) > maxBytes {
		m.Content = truncateToBytes(m.Content, maxBytes)
	}
	return m
}

func truncateToBytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}
