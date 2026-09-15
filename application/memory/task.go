package memory

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"nusashell/application/service/textsim"
	"nusashell/domain"
	"nusashell/pkg/nonce"
	clock "nusashell/pkg/time"
)

const (
	TaskMemoryAnnounceType = "task_memory"
	taskMemoryMaxHits      = 3
	taskMemoryHitChars     = 1000
	taskMemoryRecency      = 72 * time.Hour
	// TaskMemorySearchPool over-fetches from the searcher so post-search
	// recency and dedup filters still yield up to taskMemoryMaxHits hits
	// when the index contains many records.
	TaskMemorySearchPool = 12
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

// TaskMemoryHit is the exported alias for taskMemoryHit, used by the async
// semantic lane to receive filtered hits and build announcement args.
type TaskMemoryHit = taskMemoryHit

// TaskMemoryArgs builds the JSON args payload for a task_memory announcement.
// Exported wrapper for the async semantic lane.
func TaskMemoryArgs(hits []TaskMemoryHit) string { return taskMemoryArgs(hits) }

// LastUserPrompt returns the content of the most recent user message and
// true when a user message exists, or "" and false when the conversation
// has no user messages. Exported wrapper for the async semantic lane.
func LastUserPrompt(conv *domain.Conversation) (string, bool) { return lastUserPrompt(conv) }

// trivialPrompts is a small deterministic set of greetings and short
// acknowledgments that never warrant a task-memory announcement, even
// when they contain more than two effective words.
var trivialPrompts = map[string]bool{
	"hi": true, "hello": true, "hey": true, "hai": true, "halo": true,
	"thanks": true, "thank you": true, "thx": true,
	"terima kasih": true, "makasih": true,
	"sip": true, "ok": true, "oke": true, "okay": true,
}

// IsTrivialPrompt reports whether text is too short or too generic to
// warrant scanning memory for relevant task records. A prompt is trivial
// when it is empty, matches the known greeting/ack set, or contains at
// most two effective words (tokens with length >= DefaultEdgeMinTokenLen).
func IsTrivialPrompt(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return true
	}
	if trivialPrompts[normalized] {
		return true
	}
	tokens := textsim.TokenizeForOverlap(normalized, domain.DefaultEdgeMinTokenLen)
	return len(tokens) <= 2
}

// lastUserPrompt returns the content of the most recent user message and
// true when a user message exists, or "" and false when the conversation
// has no user messages.
func lastUserPrompt(conv *domain.Conversation) (string, bool) {
	if conv == nil {
		return "", false
	}
	for i := len(conv.Messages) - 1; i >= 0; i-- {
		if conv.Messages[i].Role == domain.RoleUser {
			return conv.Messages[i].Content, true
		}
	}
	return "", false
}

// MaybeAnnounceTaskMemory publishes recently confirmed records that are
// relevant to the conversation title/workspace. Selection uses the Searcher
// port (BM25, embedding off, no graph expansion) when wired; otherwise it
// falls back to a local token-overlap heuristic with minTokenLen 3. The
// 72-hour recency window and LastAnnouncedRecords dedup are applied as
// post-filters in both paths.
//
// The announcement is queued directly onto conversation.PendingAnnouncements
// (via QueueAnnouncement) and the dedup markers are updated in place on
// conversation.LastAnnouncedRecords. The caller is responsible for persisting
// the conversation (e.g. via repo.Save). This eliminates the 1–2 turn lag of
// the old finishTurn path: when called from AddTurnMessages before the
// pending-announcement drain, the scan result is drained in the same turn.
// now is injected so root tests can override clockNow without importing this
// package's clock.
func (s *Service) MaybeAnnounceTaskMemory(conversation *domain.Conversation, now func() time.Time) {
	if s == nil || s.deps.Records == nil || conversation == nil || conversation.EffectiveType() != domain.ConversationTypeConversation {
		return
	}
	if prompt, ok := lastUserPrompt(conversation); ok && IsTrivialPrompt(prompt) {
		return
	}
	if now == nil {
		now = func() time.Time { return clock.NewTime().Time() }
	}
	cutoff := now().Add(-taskMemoryRecency)
	announcedAt := map[string]time.Time{}
	for _, rec := range conversation.LastAnnouncedRecords {
		announcedAt[rec.ID] = rec.LastConfirmedAt
	}

	var selected []taskMemoryHit
	if s.deps.Searcher != nil {
		selected = s.selectViaSearcher(conversation, cutoff, announcedAt, now)
	} else {
		selected = s.selectViaFallback(conversation, cutoff, announcedAt)
	}
	if len(selected) == 0 {
		return
	}
	args := taskMemoryArgs(selected)
	msg := "Relevant task memory for this conversation is new or updated. Read the snippets; retrieve full records with memory op=search or memory op=get."
	conversation.QueueAnnouncement(domain.PendingAnnouncement{
		ID:        domain.AnnouncementToolCallPrefix + nonce.Random(),
		Type:      TaskMemoryAnnounceType,
		Args:      args,
		Message:   msg,
		CreatedAt: now(),
	})
	for _, hit := range selected {
		var confirmedAt time.Time
		if rec, err := s.deps.Records.Get(hit.ID); err == nil && rec != nil {
			confirmedAt = rec.LastConfirmed
		}
		conversation.LastAnnouncedRecords = append(conversation.LastAnnouncedRecords, domain.AnnouncedRecord{
			ID:              hit.ID,
			LastConfirmedAt: confirmedAt,
		})
	}
}

// isAlreadyAnnounced reports whether rec should be skipped because it was
// already announced and has not been re-confirmed since. A record is
// re-announced when its LastConfirmed has advanced past the stored
// LastConfirmedAt. Legacy entries (zero LastConfirmedAt, migrated from
// the old []string format) are always skipped — preserving the old
// permanent-dedup behavior for data persisted before this change.
func isAlreadyAnnounced(rec *domain.MemoryRecord, announcedAt map[string]time.Time) bool {
	stored, ok := announcedAt[rec.ID]
	if !ok {
		return false
	}
	if stored.IsZero() {
		return true
	}
	return !rec.LastConfirmed.After(stored)
}

// selectViaSearcher asks the Searcher port for ranked candidates, then
// applies the 72h recency window and LastAnnouncedRecords dedup as
// post-filters. Over-fetching (TaskMemorySearchPool) keeps the result
// correct when the index holds many records and the top hits are filtered.
func (s *Service) selectViaSearcher(conversation *domain.Conversation, cutoff time.Time, announcedAt map[string]time.Time, now func() time.Time) []taskMemoryHit {
	query := TaskMemoryQuery(conversation)
	if strings.TrimSpace(query) == "" {
		return nil
	}
	results, err := s.deps.Searcher.SearchMemory(context.Background(), query, TaskMemorySearchPool)
	if err != nil || len(results) == 0 {
		return nil
	}
	return s.filterTaskMemoryResults(conversation, results, cutoff, announcedAt)
}

// FilterTaskMemoryResults applies the 72h recency window and
// LastAnnouncedRecords dedup to pre-computed search results and returns
// the filtered hits (up to taskMemoryMaxHits). Used by the async semantic
// lane, which runs its own embedding-based search and needs the same
// post-filters as the BM25 turn-start path.
func (s *Service) FilterTaskMemoryResults(conv *domain.Conversation, results []MemorySearchResult, now func() time.Time) []TaskMemoryHit {
	if s == nil || s.deps.Records == nil || conv == nil || len(results) == 0 {
		return nil
	}
	if now == nil {
		now = func() time.Time { return clock.NewTime().Time() }
	}
	cutoff := now().Add(-taskMemoryRecency)
	announcedAt := map[string]time.Time{}
	for _, rec := range conv.LastAnnouncedRecords {
		announcedAt[rec.ID] = rec.LastConfirmedAt
	}
	return s.filterTaskMemoryResults(conv, results, cutoff, announcedAt)
}

// filterTaskMemoryResults is the shared post-filter: recency window +
// dedup + retrievability, applied to pre-computed search results.
func (s *Service) filterTaskMemoryResults(_ *domain.Conversation, results []MemorySearchResult, cutoff time.Time, announcedAt map[string]time.Time) []taskMemoryHit {
	var selected []taskMemoryHit
	for _, res := range results {
		if len(selected) >= taskMemoryMaxHits {
			break
		}
		rec, gerr := s.deps.Records.Get(res.ID)
		if gerr != nil || rec == nil || !rec.Retrievable() || isAlreadyAnnounced(rec, announcedAt) {
			continue
		}
		anchor := rec.LastConfirmed
		if anchor.IsZero() {
			anchor = rec.UpdatedAt
		}
		if anchor.Before(cutoff) {
			continue
		}
		selected = append(selected, taskMemoryHit{
			ID:      rec.ID,
			Type:    rec.Type,
			Project: rec.Scope.Project,
			Content: TruncateUTF8(rec.Body, taskMemoryHitChars),
		})
	}
	return selected
}

// selectViaFallback is the local heuristic used when no Searcher is wired
// (tests, minimal composition). It scans the catalog in order, tokenizes
// with minTokenLen 3 (textsim.TokenizeForOverlap), and matches records
// that share at least one token with the query. The old minLen 1 behavior
// is intentionally removed — short tokens like "go" or "ok" no longer
// produce spurious matches.
func (s *Service) selectViaFallback(conversation *domain.Conversation, cutoff time.Time, announcedAt map[string]time.Time) []taskMemoryHit {
	query := TaskMemoryQuery(conversation)
	queryTokens := textsim.TokenizeForOverlap(query, domain.DefaultEdgeMinTokenLen)
	if len(queryTokens) == 0 {
		return nil
	}
	var selected []taskMemoryHit
	for _, rec := range s.deps.Records.List() {
		if rec == nil || !rec.Retrievable() || isAlreadyAnnounced(rec, announcedAt) {
			continue
		}
		anchor := rec.LastConfirmed
		if anchor.IsZero() {
			anchor = rec.UpdatedAt
		}
		if anchor.Before(cutoff) {
			continue
		}
		recTokens := textsim.TokenizeForOverlap(recordSearchText(rec), domain.DefaultEdgeMinTokenLen)
		if !sharesToken(queryTokens, recTokens) {
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
	return selected
}

// sharesToken reports whether two token sets share at least one token.
func sharesToken(a, b map[string]bool) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	for t := range a {
		if b[t] {
			return true
		}
	}
	return false
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
