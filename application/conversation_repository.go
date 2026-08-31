package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

// ErrConversationImmutable is returned by ConversationRepository.Save when
// message IDs would be inserted, reordered, or deleted. Append with Add, or
// start a new epoch with ResetTranscript (compaction) / NewConversation.
var ErrConversationImmutable = errors.New("conversation transcript is immutable")

// ConversationRepository is the sanctioned write path for one conversation.
// NewConversation is the only constructor. GetById loads an existing room.
// Transcript growth is Add-only; Save persists without taking a payload.
type ConversationRepository struct {
	store        ConversationStore
	inner        *domain.Conversation
	persistedIDs []string
	epochReset   bool
}

// NewConversation creates a new in-memory conversation. Call Save to persist.
func NewConversation(store ConversationStore, title string) *ConversationRepository {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Untitled"
	}
	return &ConversationRepository{
		store: store,
		inner: domain.NewConversation(domain.NewID("conv"), title),
	}
}

// bindConversation wraps an already-loaded conversation so Add/Save go
// through the repository. Callers must bind before mutating messages so
// Save can compare against the ID prefix at load time.
func bindConversation(store ConversationStore, c *domain.Conversation) *ConversationRepository {
	r := &ConversationRepository{store: store, inner: c}
	r.rememberPersistedIDs()
	return r
}

func (r *ConversationRepository) rememberPersistedIDs() {
	if r == nil || r.inner == nil {
		r.persistedIDs = nil
		return
	}
	r.persistedIDs = messageIDs(r.inner.Messages)
}

// Conversation returns the live aggregate. Metadata may be updated in place.
// Message IDs must only grow via Add (or ResetTranscript for compaction).
func (r *ConversationRepository) Conversation() *domain.Conversation {
	if r == nil {
		return nil
	}
	return r.inner
}

// ID returns the current conversation id.
func (r *ConversationRepository) ID() string {
	if r == nil || r.inner == nil {
		return ""
	}
	return r.inner.ID
}

// GetAll returns a copy of every message in the current conversation.
func (r *ConversationRepository) GetAll() []domain.Message {
	n := 0
	if r != nil && r.inner != nil {
		n = len(r.inner.Messages)
	}
	return r.GetFrom(0, n)
}

// GetFrom returns messages in the half-open range [start, end). Out-of-range
// bounds are clamped; an inverted range yields an empty slice.
func (r *ConversationRepository) GetFrom(start, end int) []domain.Message {
	if r == nil || r.inner == nil {
		return []domain.Message{}
	}
	msgs := r.inner.Messages
	n := len(msgs)
	if start < 0 {
		start = 0
	}
	if end > n {
		end = n
	}
	if start >= end {
		return []domain.Message{}
	}
	out := make([]domain.Message, end-start)
	copy(out, msgs[start:end])
	return out
}

// GetById loads an existing conversation from the store into this repository.
// It does not create a new conversation.
func (r *ConversationRepository) GetById(id string) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("conversation store is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("conversation id is required")
	}
	c, err := r.store.Get(id)
	if err != nil {
		return err
	}
	r.inner = cloneConversation(c)
	r.epochReset = false
	r.rememberPersistedIDs()
	return nil
}

// Add appends a message with the given role. Optional args are content
// strings, attachments, tool calls, a message status, or a domain.Message
// template (compaction retention, predetermined IDs).
func (r *ConversationRepository) Add(role domain.MessageRole, args ...any) error {
	if r == nil || r.inner == nil {
		return fmt.Errorf("conversation is required")
	}
	switch role {
	case domain.RoleUser, domain.RoleAssistant, domain.RoleSystem:
	default:
		return fmt.Errorf("unknown message role %q", role)
	}
	msg := domain.Message{
		ID:        domain.NewID("msg"),
		Role:      role,
		CreatedAt: time.Now().UTC(),
		Status:    domain.StatusDone,
	}
	templated := false
	for _, arg := range args {
		if copied, ok := arg.(domain.Message); ok {
			msg = copied
			msg.Role = role
			if msg.ID == "" {
				msg.ID = domain.NewID("msg")
			}
			if msg.CreatedAt.IsZero() {
				msg.CreatedAt = time.Now().UTC()
			}
			templated = true
			continue
		}
		if err := applyAddArg(&msg, arg); err != nil {
			return err
		}
	}
	if !templated && msg.Status == "" {
		msg.Status = domain.StatusDone
	}
	r.inner.AddMessage(msg)
	return nil
}

// ResetTranscript starts a new epoch on this conversation identity (same ID
// so journal, todos, attachments, chunks, and the open room stay attached).
// Callers Add the handover, hydration, and retained suffix, then Save.
func (r *ConversationRepository) ResetTranscript() {
	if r == nil || r.inner == nil {
		return
	}
	r.inner.Messages = nil
	r.inner.Summary = ""
	r.inner.Touch()
	r.persistedIDs = nil
	r.epochReset = true
}

// Save persists the current conversation. Message IDs already persisted must
// remain a prefix of the in-memory transcript unless ResetTranscript ran.
func (r *ConversationRepository) Save() error {
	if r == nil || r.inner == nil {
		return fmt.Errorf("conversation is required")
	}
	if r.store == nil {
		return fmt.Errorf("conversation store is required")
	}
	if !r.epochReset && !transcriptIDsAppendOnly(r.persistedIDs, r.inner.Messages) {
		return ErrConversationImmutable
	}
	if err := r.store.Save(cloneConversation(r.inner)); err != nil {
		return err
	}
	r.epochReset = false
	r.rememberPersistedIDs()
	return nil
}

func applyAddArg(msg *domain.Message, arg any) error {
	switch v := arg.(type) {
	case string:
		msg.Content += v
	case domain.Attachment:
		msg.Attachments = append(msg.Attachments, v)
	case []domain.Attachment:
		msg.Attachments = append(msg.Attachments, v...)
	case domain.ToolCall:
		msg.ToolCalls = append(msg.ToolCalls, v)
	case []domain.ToolCall:
		msg.ToolCalls = append(msg.ToolCalls, v...)
	case domain.MessageStatus:
		msg.Status = v
	default:
		return fmt.Errorf("unsupported Add argument %T", arg)
	}
	return nil
}

func transcriptIDsAppendOnly(persisted []string, current []domain.Message) bool {
	if len(current) < len(persisted) {
		return false
	}
	for i, id := range persisted {
		if current[i].ID != id {
			return false
		}
	}
	return true
}

func messageIDs(msgs []domain.Message) []string {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.ID
	}
	return out
}

func cloneConversation(c *domain.Conversation) *domain.Conversation {
	if c == nil {
		return nil
	}
	b, err := json.Marshal(c)
	if err != nil {
		cp := *c
		return &cp
	}
	var out domain.Conversation
	if err := json.Unmarshal(b, &out); err != nil {
		cp := *c
		return &cp
	}
	return &out
}

func (a *App) loadRepo(id string) (*ConversationRepository, error) {
	if a == nil || a.Conversations == nil {
		return nil, fmt.Errorf("conversation store is required")
	}
	c, err := a.Conversations.Get(id)
	if err != nil {
		return nil, err
	}
	return bindConversation(a.Conversations, c), nil
}

func (a *App) loadRepoRPC(id string) (*ConversationRepository, *contracts.RPCError) {
	c, rpcErr := a.getConversation(id)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return bindConversation(a.Conversations, c), nil
}
