package application

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"nusashell/domain"
	clock "nusashell/pkg/time"
)

const (
	taskMemoryAnnounceType = "task_memory"
	taskMemoryMaxHits      = 3
	taskMemoryHitChars     = 300
	taskMemoryRecency      = 72 * time.Hour
)

func taskMemoryArgs(hits []taskMemoryHit) string {
	b, err := json.Marshal(struct {
		Type string          `json:"type"`
		Hits []taskMemoryHit `json:"hits"`
	}{Type: taskMemoryAnnounceType, Hits: hits})
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

func (a *App) maybeAnnounceTaskMemory(conversationID string, conversation *domain.Conversation) {
	// Job transcripts (background learning, automation steps) are not rooms:
	// announcing task memory into them would write records from a job's own
	// output instead of the user's conversation.
	if a.MemoryRecords == nil || conversation == nil || conversation.EffectiveType() != domain.ConversationTypeConversation {
		return
	}
	queryWords := taskMemoryAlphaWords(taskMemoryQuery(conversation))
	if len(queryWords) == 0 {
		return
	}
	cutoff := clockNow().Add(-taskMemoryRecency)
	known := map[string]bool{}
	for _, id := range conversation.LastAnnouncedRecords {
		known[id] = true
	}
	var selected []taskMemoryHit
	for _, rec := range a.MemoryRecords.List() {
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
		if !taskMemorySharesWord(queryWords, taskMemoryAlphaWords(recordSearchText(rec))) {
			continue
		}
		selected = append(selected, taskMemoryHit{
			ID:      rec.ID,
			Type:    rec.Type,
			Project: rec.Scope.Project,
			Content: truncateUTF8(rec.Body, taskMemoryHitChars),
		})
		if len(selected) >= taskMemoryMaxHits {
			break
		}
	}
	if len(selected) == 0 {
		return
	}
	msg := "Relevant task memory for this conversation is new or updated. Read the snippets; retrieve full records with memory op=search or memory op=get."
	a.publishAnnouncement(conversationID, newAnnouncement(taskMemoryAnnounceType, taskMemoryArgs(selected), msg))
	for _, hit := range selected {
		conversation.LastAnnouncedRecords = append(conversation.LastAnnouncedRecords, hit.ID)
	}
	if repo, err := a.loadRepo(conversationID); err == nil {
		repo.Conversation().LastAnnouncedRecords = conversation.LastAnnouncedRecords
		_ = repo.Save()
	}
}

func taskMemoryQuery(conversation *domain.Conversation) string {
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

// taskMemoryAlphaWords extracts lowercase [a-zA-Z]+ tokens. Punctuation,
// digits, emoji, and other non-letters are separators, so an empty
// workspace (filepath.Base("") == ".") cannot match a period in a body.
func taskMemoryAlphaWords(s string) map[string]bool {
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

var clockNow = func() time.Time {
	return clock.NewTime().Time()
}

func truncateUTF8(s string, max int) string {
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
