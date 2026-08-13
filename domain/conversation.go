package domain

import "time"

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
}

type Conversation struct {
	ID        string
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
	Model     string
	Status    string // idle | running
	Summary   string // compaction summary, "" when never compacted
	Workspace string // optional absolute working directory selected for this conversation
	Messages  []Message
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
			s := m.Content
			if len(s) > 48 {
				s = s[:48] + "…"
			}
			return s
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
		total += EstimateTokens(m.Content)
		total += EstimateTokens(m.Reasoning)
		if m.Usage != nil {
			total += m.Usage.InputTokens + m.Usage.OutputTokens
		}
		for _, tc := range m.ToolCalls {
			total += EstimateTokens(tc.Name) + EstimateTokens(tc.Args) + EstimateTokens(tc.Output)
		}
		for _, s := range m.Steps {
			total += EstimateTokens(s.Content)
			for _, tc := range s.ToolCalls {
				total += EstimateTokens(tc.Name) + EstimateTokens(tc.Args) + EstimateTokens(tc.Output)
			}
		}
		for _, attachment := range m.Attachments {
			total += EstimateTokens(attachment.Name)
			total += EstimateTokens(attachment.Content)
			total += EstimateTokens(attachment.DataURL)
		}
	}
	if c.Summary != "" {
		total += EstimateTokens(c.Summary)
	}
	return total
}

// Compact replaces the oldest messages with a summary marker message and
// keeps the most recent keep messages (including any that are not done).
func (c *Conversation) Compact(summary string, keep int) {
	if len(c.Messages) <= keep+1 {
		keep = len(c.Messages) - 1
		if keep < 0 {
			keep = 0
		}
	}
	recent := c.Messages[len(c.Messages)-keep:]
	if c.Summary != "" {
		c.Summary += "\n\n" + summary
	} else {
		c.Summary = summary
	}
	marker := Message{
		ID:        NewID("msg"),
		Role:      RoleSystem,
		Content:   "Earlier conversation history was compacted. Summary:\n" + c.Summary,
		CreatedAt: time.Now().UTC(),
		Status:    StatusDone,
	}
	c.Messages = append([]Message{marker}, recent...)
	c.Touch()
}
