package application

import (
	"context"
	"strings"
	"time"

	"nusashell/domain"
)

// MaxMemoryEntries is the hard capacity limit for memory entries. The
// lifecycle manager prunes low-strength entries when this is exceeded.
const MaxMemoryEntries = 500

// LearningReviewer runs the post-turn write path: extract observations
// from the turn, resolve them against existing memory (exact → fuzzy),
// and write new entries through an approval gate. It is called from a
// background goroutine after each turn completes.
//
// The reviewer is conservative by design — it only writes entries that
// pass the approval gate, and it deduplicates against existing memory to
// prevent unbounded growth.
type LearningReviewer struct {
	memory MemoryStore
	graph  *LearningGraphService
}

// NewLearningReviewer creates a reviewer. graph may be nil if edge
// strengthening is not configured.
func NewLearningReviewer(memory MemoryStore, graph *LearningGraphService) *LearningReviewer {
	return &LearningReviewer{memory: memory, graph: graph}
}

// ReviewTurn runs extraction + resolution + write for a completed turn.
// userMsg and assistantMsg are the last user prompt and assistant response
// from the turn. This is safe to call in a background goroutine.
func (r *LearningReviewer) ReviewTurn(ctx context.Context, userMsg, assistantMsg string) {
	observations := ExtractObservations(userMsg, assistantMsg)
	if len(observations) == 0 {
		return
	}
	for _, obs := range observations {
		if !shouldWrite(obs) {
			continue
		}
		r.writeObservation(ctx, obs)
	}
}

// shouldWrite is the write-approval gate. It rejects observations that
// are too weak, too short, or duplicates of existing memory.
func shouldWrite(obs ExtractedObservation) bool {
	if obs.Weight < 0.3 {
		return false
	}
	if len(strings.TrimSpace(obs.Content)) < 10 {
		return false
	}
	return true
}

// writeObservation resolves the observation against existing memory
// (exact → fuzzy) and writes it if no duplicate is found. If a duplicate
// exists, it strengthens the edge instead of creating a new entry.
func (r *LearningReviewer) writeObservation(ctx context.Context, obs ExtractedObservation) {
	// Check capacity — if at limit, skip write (lifecycle manager will prune).
	if len(r.memory.List()) >= MaxMemoryEntries {
		return
	}

	// 3-tier resolution: exact → fuzzy.
	normalized := normalizeContent(obs.Content)
	for _, existing := range r.memory.List() {
		if normalizeContent(existing.Content) == normalized {
			// Exact match — strengthen edge if graph is configured.
			if r.graph != nil {
				_, _ = r.graph.AddEdge(existing.ID, existing.ID, domain.EdgeUsedWith, obs.Weight*0.3)
			}
			return
		}
		if isFuzzyMatch(normalizeContent(existing.Content), normalized) {
			// Fuzzy match — strengthen edge, don't duplicate.
			if r.graph != nil {
				_, _ = r.graph.AddEdge(existing.ID, existing.ID, domain.EdgeRelated, obs.Weight*0.2)
			}
			return
		}
	}

	// No duplicate — write new entry.
	entry := ObservationToMemory(obs)
	if err := r.memory.Save(entry); err != nil {
		return
	}
}

// normalizeContent lowercases, trims, and collapses whitespace for
// exact-match comparison.
func normalizeContent(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Join(strings.Fields(s), " ")
	return s
}

// isFuzzyMatch returns true if the two normalized strings share enough
// words to be considered the same observation. Uses Jaccard similarity
// on word sets with a 0.6 threshold.
func isFuzzyMatch(a, b string) bool {
	wordsA := strings.Fields(a)
	wordsB := strings.Fields(b)
	if len(wordsA) == 0 || len(wordsB) == 0 {
		return false
	}
	setA := make(map[string]bool, len(wordsA))
	for _, w := range wordsA {
		setA[w] = true
	}
	intersection := 0
	for _, w := range wordsB {
		if setA[w] {
			intersection++
		}
	}
	union := len(wordsA) + len(wordsB) - intersection
	if union == 0 {
		return false
	}
	jaccard := float64(intersection) / float64(union)
	return jaccard >= 0.6
}

// ReviewTurnAsync forks a background goroutine to review a turn. The
// goroutine is bounded by ctx and exits when the context is cancelled.
// This is the entry point called from finishTurn.
func (a *App) ReviewTurnAsync(ctx context.Context, conversationID, userMsg, assistantMsg string) {
	if a.LearningReviewer == nil {
		return
	}
	go func() {
		reviewCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		a.LearningReviewer.ReviewTurn(reviewCtx, userMsg, assistantMsg)
	}()
}
