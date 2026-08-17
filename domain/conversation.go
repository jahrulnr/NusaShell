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
	StatusInterrupted MessageStatus = "interrupted"
)

type ToolCallStatus string

const (
	ToolRunning     ToolCallStatus = "running"
	ToolOK          ToolCallStatus = "ok"
	ToolFailed      ToolCallStatus = "fail"
	ToolInterrupted ToolCallStatus = "interrupted"
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
	// read_image so vision-capable models can see them in the tool result.
	// Empty for all other tools. Persisted with the tool call so subsequent
	// rounds and conversation reloads keep the images visible.
	OutputAttachments []Attachment
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
	Usage          *Usage
	CreatedAt      time.Time
	Status         MessageStatus
	Error          string
	ToolCalls      []ToolCall // all tool calls (mirrors tool_calls steps, for backward compat)
	Attachments    []Attachment
	Steer          bool // true for user messages injected mid-turn (steering)
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

// Compact replaces the oldest messages with a summary marker message and
// keeps the most recent messages that fit within keepTokenBudget. Tool calls
// and reasoning are stripped from retained messages (they are already captured
// in the summary). The last message that does not fit fully is truncated
// per-content rather than dropped entirely.
func (c *Conversation) Compact(summary string, keepTokenBudget int) {
	if c.Summary != "" {
		c.Summary += "\n\n" + summary
	} else {
		c.Summary = summary
	}
	marker := Message{
		ID:   NewID("msg"),
		Role: RoleSystem,
		Content: "Another language model started to solve this problem and produced a summary of its thinking process. " +
			"Use this to build on the work that has already been done and avoid duplicating work.\n\n" +
			"Compacted context handover:\n" + c.Summary,
		CreatedAt: time.Now().UTC(),
		Status:    StatusDone,
	}

	// Iterate backward from the most recent message, keeping messages until
	// the token budget is exhausted. The last message that does not fit is
	// truncated rather than dropped.
	remaining := keepTokenBudget
	var retained []Message
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if remaining <= 0 {
			break
		}
		msg := StripForRetention(c.Messages[i])
		tokens := msg.EstimateTokens()
		if tokens <= remaining {
			retained = append([]Message{msg}, retained...)
			remaining -= tokens
		} else {
			// Truncate content to fit the remaining budget.
			msg = truncateMessageContent(msg, remaining)
			if msg.Content != "" || len(msg.Attachments) > 0 {
				retained = append([]Message{msg}, retained...)
			}
			remaining = 0
		}
	}

	c.Messages = append([]Message{marker}, retained...)
	c.Touch()
}

// ArchiveMessages returns the messages that would be dropped by a compaction
// with the given keep-token budget. The returned slice preserves full message
// content (tool calls, reasoning, steps) so it can be archived for later
// scroll-back retrieval. Call this before Compact to capture what will be
// dropped. Returns nil if nothing would be archived (everything fits).
func (c *Conversation) ArchiveMessages(keepTokenBudget int) []Message {
	remaining := keepTokenBudget
	retainedCount := 0
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if remaining <= 0 {
			break
		}
		msg := StripForRetention(c.Messages[i])
		tokens := msg.EstimateTokens()
		if tokens <= remaining {
			retainedCount++
			remaining -= tokens
		} else {
			retainedCount++ // truncated but still retained
			remaining = 0
		}
	}
	if retainedCount >= len(c.Messages) {
		return nil
	}
	archived := make([]Message, len(c.Messages)-retainedCount)
	copy(archived, c.Messages[:len(c.Messages)-retainedCount])
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
