package conversation

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"nusashell/domain"
)

const (
	defaultReadTurnWindow  = 5
	readToolOutputMaxRunes = 2000
	summaryPreviewMaxRunes = 400
	searchSnippetMaxRunes  = 240
)

// InfoDTO is the yamlBlock payload for conversation(op=info).
type InfoDTO struct {
	ID             string `json:"id" yaml:"id"`
	Title          string `json:"title" yaml:"title"`
	Status         string `json:"status,omitempty" yaml:"status,omitempty"`
	Type           string `json:"type,omitempty" yaml:"type,omitempty"`
	Workspace      string `json:"workspace,omitempty" yaml:"workspace,omitempty"`
	Model          string `json:"model,omitempty" yaml:"model,omitempty"`
	CreatedAt      string `json:"created_at" yaml:"created_at"`
	UpdatedAt      string `json:"updated_at" yaml:"updated_at"`
	MessageCount   int    `json:"message_count" yaml:"message_count"`
	TurnCount      int    `json:"turn_count" yaml:"turn_count"`
	ChunkCount     int    `json:"chunk_count" yaml:"chunk_count"`
	ChunkIndex     *int   `json:"chunk_index,omitempty" yaml:"chunk_index,omitempty"`
	HasSummary     bool   `json:"has_summary" yaml:"has_summary"`
	SummaryPreview string `json:"summary_preview,omitempty" yaml:"summary_preview,omitempty"`
	ContextTokens  int64  `json:"context_tokens,omitempty" yaml:"context_tokens,omitempty"`
}

// ReadMsgDTO is one JSONL line for conversation(op=read).
type ReadMsgDTO struct {
	Turn    int           `json:"turn"`
	ID      string        `json:"id"`
	Role    string        `json:"role"`
	Content string        `json:"content,omitempty"`
	Status  string        `json:"status,omitempty"`
	Error   string        `json:"error,omitempty"`
	Tools   []ReadToolDTO `json:"tools,omitempty"`
}

// ReadToolDTO is a compact tool-call projection for transcript reads.
type ReadToolDTO struct {
	Name   string `json:"name"`
	Status string `json:"status,omitempty"`
	Output string `json:"output,omitempty"`
}

// ReadResult is the meta + messages returned by RoomRead.
type ReadResult struct {
	ID         string
	ChunkIndex *int
	Start      int
	End        int
	TurnCount  int
	Messages   []ReadMsgDTO
}

// MessageHitDTO is one JSONL line for conversation(op=search) with id scope.
type MessageHitDTO struct {
	ConversationID string `json:"conversation_id"`
	Turn           int    `json:"turn"`
	MessageID      string `json:"message_id"`
	Role           string `json:"role"`
	Snippet        string `json:"snippet"`
}

// RoomInfo returns compact metadata for a conversation or one archived chunk.
// chunk nil = active transcript; otherwise that 0-based archive index.
func (s *Service) RoomInfo(id string, chunk *int) (InfoDTO, error) {
	c, err := s.requireConversation(id)
	if err != nil {
		return InfoDTO{}, err
	}
	msgs, err := s.messagesForScope(c, chunk)
	if err != nil {
		return InfoDTO{}, err
	}
	visible := visibleTranscript(msgs)
	turns := partitionTurns(visible)
	info := InfoDTO{
		ID:             c.ID,
		Title:          c.Title,
		Status:         c.Status,
		Type:           string(c.EffectiveType()),
		Workspace:      c.Workspace,
		Model:          c.Model,
		CreatedAt:      c.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:      c.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		MessageCount:   len(visible),
		TurnCount:      len(turns),
		ChunkCount:     c.ChunkCount,
		ChunkIndex:     chunk,
		HasSummary:     strings.TrimSpace(c.Summary) != "",
		SummaryPreview: clipRunes(strings.TrimSpace(c.Summary), summaryPreviewMaxRunes),
		ContextTokens:  c.ContextTokens,
	}
	return info, nil
}

// RoomRead returns visible messages for turns [start, end] inclusive.
// When start and end are both nil, returns the last defaultReadTurnWindow turns.
func (s *Service) RoomRead(id string, chunk *int, start, end *int) (ReadResult, error) {
	c, err := s.requireConversation(id)
	if err != nil {
		return ReadResult{}, err
	}
	msgs, err := s.messagesForScope(c, chunk)
	if err != nil {
		return ReadResult{}, err
	}
	visible := visibleTranscript(msgs)
	turns := partitionTurns(visible)
	turnCount := len(turns)
	sIdx, eIdx, err := resolveTurnWindow(start, end, turnCount)
	if err != nil {
		return ReadResult{}, err
	}
	out := make([]ReadMsgDTO, 0)
	for ti := sIdx; ti <= eIdx; ti++ {
		for _, m := range turns[ti] {
			out = append(out, projectReadMessage(ti, m))
		}
	}
	return ReadResult{
		ID:         c.ID,
		ChunkIndex: chunk,
		Start:      sIdx,
		End:        eIdx,
		TurnCount:  turnCount,
		Messages:   out,
	}, nil
}

// SearchMessages finds user/assistant text hits inside one conversation's
// active transcript (hydration filtered). Snippets only — use RoomRead for
// full turn bodies.
func (s *Service) SearchMessages(id, query string, limit, offset int) (int, []MessageHitDTO, error) {
	c, err := s.requireConversation(id)
	if err != nil {
		return 0, nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return 0, nil, fmt.Errorf("query is required")
	}
	visible := visibleTranscript(c.Messages)
	turns := partitionTurns(visible)
	hits := make([]MessageHitDTO, 0)
	for ti, turn := range turns {
		for _, m := range turn {
			if m.Role != domain.RoleUser && m.Role != domain.RoleAssistant {
				continue
			}
			text := strings.TrimSpace(m.Content)
			if text == "" || !strings.Contains(strings.ToLower(text), q) {
				continue
			}
			hits = append(hits, MessageHitDTO{
				ConversationID: c.ID,
				Turn:           ti,
				MessageID:      m.ID,
				Role:           string(m.Role),
				Snippet:        clipRunes(text, searchSnippetMaxRunes),
			})
		}
	}
	total := len(hits)
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return total, []MessageHitDTO{}, nil
	}
	if limit <= 0 {
		limit = 20
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return total, hits[offset:end], nil
}

func (s *Service) requireConversation(id string) (*domain.Conversation, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("conversation id is required")
	}
	if s.store == nil {
		return nil, fmt.Errorf("conversation store not available")
	}
	c, err := s.store.Get(id)
	if err != nil || c == nil {
		return nil, fmt.Errorf("conversation %q not found", id)
	}
	return c, nil
}

func (s *Service) messagesForScope(c *domain.Conversation, chunk *int) ([]domain.Message, error) {
	if chunk == nil {
		return c.Messages, nil
	}
	if *chunk < 0 {
		return nil, fmt.Errorf("chunk index must be >= 0")
	}
	if c.ChunkCount <= 0 {
		return nil, fmt.Errorf("conversation %q has no archived chunks", c.ID)
	}
	if *chunk >= c.ChunkCount {
		return nil, fmt.Errorf("chunk %d not found (chunk_count=%d)", *chunk, c.ChunkCount)
	}
	msgs, err := s.store.GetChunk(c.ID, *chunk)
	if err != nil {
		return nil, fmt.Errorf("chunk %d not found: %w", *chunk, err)
	}
	return msgs, nil
}

// visibleTranscript drops hydration-only messages (same rule as HandleGet).
func visibleTranscript(msgs []domain.Message) []domain.Message {
	out := make([]domain.Message, 0, len(msgs))
	for _, m := range msgs {
		if domain.IsHydrationMessage(m) {
			continue
		}
		out = append(out, domain.FilterHydrationToolCalls(m))
	}
	return out
}

// partitionTurns groups visible messages into 0-based turns: each turn starts
// at a non-steer user message and includes following assistants (and mid-turn
// steer users) until the next real user. Leading assistants (before any user)
// form turn 0. Steer users never open a new turn index.
func partitionTurns(msgs []domain.Message) [][]domain.Message {
	if len(msgs) == 0 {
		return nil
	}
	turns := make([][]domain.Message, 0)
	var cur []domain.Message
	started := false
	for _, m := range msgs {
		if m.Role == domain.RoleUser && !m.Steer {
			if started && len(cur) > 0 {
				turns = append(turns, cur)
			}
			cur = []domain.Message{m}
			started = true
			continue
		}
		if !started {
			cur = []domain.Message{m}
			started = true
			continue
		}
		cur = append(cur, m)
	}
	if len(cur) > 0 {
		turns = append(turns, cur)
	}
	return turns
}

func resolveTurnWindow(start, end *int, turnCount int) (int, int, error) {
	if turnCount <= 0 {
		return 0, -1, nil // empty: start=0 end=-1 signals no messages
	}
	if start == nil && end == nil {
		s := turnCount - defaultReadTurnWindow
		if s < 0 {
			s = 0
		}
		return s, turnCount - 1, nil
	}
	s := 0
	if start != nil {
		s = *start
	}
	e := turnCount - 1
	if end != nil {
		e = *end
	} else if start != nil {
		e = *start
	}
	if s < 0 {
		return 0, 0, fmt.Errorf("start must be >= 0")
	}
	if e < s {
		return 0, 0, fmt.Errorf("end must be >= start")
	}
	if s >= turnCount {
		return 0, 0, fmt.Errorf("start %d out of range (turn_count=%d)", s, turnCount)
	}
	if e >= turnCount {
		e = turnCount - 1
	}
	return s, e, nil
}

func projectReadMessage(turn int, m domain.Message) ReadMsgDTO {
	dto := ReadMsgDTO{
		Turn:    turn,
		ID:      m.ID,
		Role:    string(m.Role),
		Content: m.Content,
		Status:  string(m.Status),
		Error:   m.Error,
	}
	if len(m.ToolCalls) > 0 {
		tools := make([]ReadToolDTO, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			tools = append(tools, ReadToolDTO{
				Name:   tc.Name,
				Status: string(tc.Status),
				Output: clipRunes(tc.Output, readToolOutputMaxRunes),
			})
		}
		dto.Tools = tools
	}
	return dto
}

func clipRunes(s string, max int) string {
	if max <= 0 || s == "" {
		return s
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "…"
}

// messageTextMatch reports whether any visible user/assistant content contains q
// (already lowercased). Used by room search to surface body matches.
func messageTextMatch(msgs []domain.Message, q string) bool {
	if q == "" {
		return false
	}
	for _, m := range visibleTranscript(msgs) {
		if m.Role != domain.RoleUser && m.Role != domain.RoleAssistant {
			continue
		}
		if strings.Contains(strings.ToLower(m.Content), q) {
			return true
		}
	}
	return false
}
