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
	if a.MemoryRecords == nil || conversation == nil || conversation.Origin == domain.ConversationOriginPipeline {
		return
	}
	query := strings.ToLower(taskMemoryQuery(conversation))
	if strings.TrimSpace(query) == "" {
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
		blob := strings.ToLower(recordSearchText(rec))
		if query != "" && !strings.Contains(blob, query) && !strings.Contains(blob, strings.ToLower(filepath.Base(conversation.Workspace))) {
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
