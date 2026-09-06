package memory

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"nusashell/domain"
	clock "nusashell/pkg/time"
)

const (
	TaskMemoryAnnounceType = "task_memory"
	taskMemoryMaxHits      = 3
	taskMemoryHitChars     = 300
	taskMemoryRecency      = 72 * time.Hour
)

func taskMemoryArgs(hits []taskMemoryHit) string {
	b, err := json.Marshal(struct {
		Type string          `json:"type"`
		Hits []taskMemoryHit `json:"hits"`
	}{Type: TaskMemoryAnnounceType, Hits: hits})
	if err != nil {
		return `{"type":"task_memory","hits":[]}`
	}
	return string(b)
}

type taskMemoryHit struct {
	ID      string `json:"id"`
	Type    string `json:"type,omitempty"`
	Project string `json:"project,omitempty"`
	Content string `json:"content"`
}

// MaybeAnnounceTaskMemory publishes recently confirmed records that share
// an alphabetic word with the conversation title/workspace. now is injected
// so root tests can override clockNow without importing this package's clock.
func (s *Service) MaybeAnnounceTaskMemory(conversationID string, conversation *domain.Conversation, now func() time.Time) {
	if s == nil || s.deps.Records == nil || conversation == nil || conversation.EffectiveType() != domain.ConversationTypeConversation {
		return
	}
	queryWords := TaskMemoryAlphaWords(TaskMemoryQuery(conversation))
	if len(queryWords) == 0 {
		return
	}
	if now == nil {
		now = func() time.Time { return clock.NewTime().Time() }
	}
	cutoff := now().Add(-taskMemoryRecency)
	known := map[string]bool{}
	for _, id := range conversation.LastAnnouncedRecords {
		known[id] = true
	}
	var selected []taskMemoryHit
	for _, rec := range s.deps.Records.List() {
		if rec == nil || !rec.Retrievable() || known[rec.ID] {
			continue
		}
		anchor := rec.LastConfirmed
		if anchor.IsZero() {
			anchor = rec.UpdatedAt
		}
		if anchor.Before(cutoff) {
			continue
		}
		if !taskMemorySharesWord(queryWords, TaskMemoryAlphaWords(recordSearchText(rec))) {
			continue
		}
		selected = append(selected, taskMemoryHit{
			ID:      rec.ID,
			Type:    rec.Type,
			Project: rec.Scope.Project,
			Content: TruncateUTF8(rec.Body, taskMemoryHitChars),
		})
		if len(selected) >= taskMemoryMaxHits {
			break
		}
	}
	if len(selected) == 0 {
		return
	}
	msg := "Relevant task memory for this conversation is new or updated. Read the snippets; retrieve full records with memory op=search or memory op=get."
	if s.deps.Announce != nil {
		s.deps.Announce(conversationID, TaskMemoryAnnounceType, taskMemoryArgs(selected), msg)
	}
	for _, hit := range selected {
		conversation.LastAnnouncedRecords = append(conversation.LastAnnouncedRecords, hit.ID)
	}
	if s.deps.PersistAnnounced != nil {
		_ = s.deps.PersistAnnounced(conversationID, conversation.LastAnnouncedRecords)
	}
}

func TaskMemoryQuery(conversation *domain.Conversation) string {
	parts := make([]string, 0, 2)
	if conversation == nil {
		return ""
	}
	if title := strings.TrimSpace(conversation.Title); title != "" {
		parts = append(parts, title)
	}
	if ws := strings.TrimSpace(conversation.Workspace); ws != "" {
		parts = append(parts, filepath.Base(ws))
	}
	return strings.Join(parts, " ")
}

// TaskMemoryAlphaWords extracts lowercase [a-zA-Z]+ tokens. Punctuation,
// digits, emoji, and other non-letters are separators, so an empty
// workspace (filepath.Base("") == ".") cannot match a period in a body.
func TaskMemoryAlphaWords(s string) map[string]bool {
	words := map[string]bool{}
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		words[b.String()] = true
		b.Reset()
	}
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return words
}

func taskMemorySharesWord(query, blob map[string]bool) bool {
	if len(query) == 0 || len(blob) == 0 {
		return false
	}
	for w := range query {
		if blob[w] {
			return true
		}
	}
	return false
}

func TruncateUTF8(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// recordSearchText is a local copy of the learner helper (learning_search.go).
// Feature packages never import each other.
func recordSearchText(e *domain.MemoryRecord) string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(strings.Join([]string{e.Body, e.Subject, e.Predicate, e.Object, e.Type, e.Scope.Project}, " "))
}
