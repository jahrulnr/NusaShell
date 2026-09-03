package application

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"nusashell/domain"
)

// Task-memory announcements deliver fragments that are relevant to one
// conversation and new/updated since they were last announced. They ride
// the shared `announcement` channel (type "task_memory") with the snippet
// contents inline, so the receiving agent gets concrete task knowledge
// without pondering whether to call memory search. Delivery is per-room
// (unlike memory_changed fan-out): each conversation tracks the fragment
// IDs it has already seen in LastAnnouncedFragments, so a fragment is
// announced once per room, across turns and restarts.

const (
	// taskMemoryAnnounceType is the announcement args type for task
	// memory deliveries.
	taskMemoryAnnounceType = "task_memory"
	// taskMemoryMaxHits caps how many fragments ride one announcement.
	taskMemoryMaxHits = 3
	// taskMemoryHitChars truncates each fragment body in the payload.
	taskMemoryHitChars = 300
	// taskMemoryRecency limits candidates to fragments touched recently.
	taskMemoryRecency = 72 * time.Hour
)

// taskMemoryArgs is the self-describing announcement payload: type plus
// the ranked hits with snippet contents.
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
	ID       string `json:"id"`
	Category string `json:"category,omitempty"`
	Project  string `json:"project,omitempty"`
	Task     string `json:"task,omitempty"`
	Content  string `json:"content"`
}

// maybeAnnounceTaskMemory finds fragments relevant to this conversation
// that it has not been told about yet and publishes a task_memory
// announcement with their snippets. The delivered IDs are recorded on the
// conversation so each fragment announces once. Best-effort: failures are
// logged, never fatal to the finished turn.
func (a *App) maybeAnnounceTaskMemory(conversationID string, conversation *domain.Conversation) {
	if a.Fragments == nil || conversation == nil || conversation.Origin == domain.ConversationOriginPipeline {
		return
	}
	query := taskMemoryQuery(conversation)
	if strings.TrimSpace(query) == "" {
		return
	}
	hits := a.Fragments.Search(domain.FragmentSearchFilter{Query: query, Limit: 10})
	if len(hits) == 0 {
		return
	}
	cutoff := clockNow().Add(-taskMemoryRecency)
	known := map[string]bool{}
	for _, id := range conversation.LastAnnouncedFragments {
		known[id] = true
	}
	var selected []taskMemoryHit
	for _, h := range hits {
		if len(selected) >= taskMemoryMaxHits {
			break
		}
		frag := h.Fragment
		if frag == nil || known[frag.ID] {
			continue
		}
		if frag.UpdatedAt.Before(cutoff) {
			continue // not a new/changed fragment
		}
		selected = append(selected, taskMemoryHit{
			ID:       frag.ID,
			Category: frag.Category,
			Project:  frag.Project,
			Task:     frag.Task,
			Content:  truncateUTF8(frag.Content, taskMemoryHitChars),
		})
	}
	if len(selected) == 0 {
		return
	}
	msg := "Relevant task memory for this conversation is new or updated. Read the snippets above; they may come from another conversation or the background learning agent. Retrieve full entries with memory op=search."
	a.publishAnnouncement(conversationID, newAnnouncement(taskMemoryAnnounceType, taskMemoryArgs(selected), msg))
	// Record delivered IDs so future turns skip them, and persist that
	// state. publishAnnouncement saves its own loaded copy (the queue
	// write); this extra save carries the dedup marker. Safe here: the
	// finishTurn caller holds the conversation turn lock, so no drain can
	// interleave.
	for _, hit := range selected {
		conversation.LastAnnouncedFragments = append(conversation.LastAnnouncedFragments, hit.ID)
	}
	if repo, err := a.loadRepo(conversationID); err == nil {
		repo.Conversation().LastAnnouncedFragments = conversation.LastAnnouncedFragments
		_ = repo.Save()
	}
}

// taskMemoryQuery builds the retrieval query from the conversation title
// (the first user message) and the workspace basename, so BM25 matches
// task knowledge that belongs to this room's work.
func taskMemoryQuery(conversation *domain.Conversation) string {
	parts := make([]string, 0, 2)
	if title := strings.TrimSpace(conversation.Title); title != "" {
		words := strings.Fields(title)
		if len(words) > 12 {
			words = words[:12]
		}
		parts = append(parts, strings.Join(words, " "))
	}
	if ws := strings.TrimSpace(conversation.Workspace); ws != "" {
		parts = append(parts, filepath.Base(ws))
	}
	return strings.Join(parts, " ")
}

// truncateUTF8 cuts s to at most n bytes without splitting a UTF-8 rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && cut[len(cut)-1]&0xC0 == 0x80 {
		cut = cut[:len(cut)-1]
	}
	return cut
}

// clockNow is the current time, injectable for tests.
var clockNow = time.Now
