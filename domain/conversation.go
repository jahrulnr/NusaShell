package domain

import (
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
// URL so compatible providers can receive the exact source bytes.
type Attachment struct {
	Type      string // text | image | file
	Name      string
	MediaType string
	Content   string // text only
	DataURL   string // image and file only
}

type ToolCall struct {
	ID     string
	Name   string
	Args   string // raw JSON, as sent by the model
	Status ToolCallStatus
	Output string
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
	ID          string
	Role        MessageRole
	Content     string // final text (mirrors last text step, for backward compat)
	Reasoning   string // final reasoning (mirrors last reasoning step, for backward compat)
	Steps       []MessageStep
	Model       string
	Usage       *Usage
	CreatedAt   time.Time
	Status      MessageStatus
	Error       string
	ToolCalls   []ToolCall // all tool calls (mirrors tool_calls steps, for backward compat)
	Attachments []Attachment
	Steer       bool // true for user messages injected mid-turn (steering)
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
		total += EstimateTokens(attachment.DataURL)
	}
	return total
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
