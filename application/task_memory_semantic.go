package application

import (
	"context"
	"strings"
	"time"

	"nusashell/application/learn"
	"nusashell/application/memory"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// taskMemoryLaneTimeout is the maximum time the async semantic lane may
// spend on embedding/search. A failing embedder cannot hang the lane.
const taskMemoryLaneTimeout = 10 * time.Second

// taskMemoryLaneDeps holds the explicit dependencies for the async semantic
// lane, extracted for deterministic unit testing. In production, App
// constructs this from its lazy-initialized services.
type taskMemoryLaneDeps struct {
	// searcher is the LearningSearcher with an embedder configured.
	searcher *LearningSearcher
	// resolveEmbed returns the configured embedder and its model ID.
	// When the embedder is nil, the lane skips (BM25 turn-start is enough).
	resolveEmbed func() (Embedder, string)
	// cache is the content-addressed embedding cache (optional, nil = no cache).
	cache learn.EmbeddingCache
	// filter applies the 72h recency + dedup post-filters to search results.
	filter func(conv *domain.Conversation, results []memory.MemorySearchResult, now func() time.Time) []memory.TaskMemoryHit
	// breaker disables the lane after N consecutive failures.
	breaker *taskMemoryBreaker
	// records resolves LastConfirmed for filtered hits so the lane can
	// build dedup markers (ID + LastConfirmedAt) for the publish callback.
	records memory.RecordStore
	// publish queues a task_memory announcement onto the conversation,
	// together with the dedup markers for the published hits. The
	// callback is expected to write markers atomically under the
	// announcementLock (e.g. via agent.Service.PublishTaskMemoryAnnouncement)
	// so the turn-start scan does not re-announce the same records.
	publish func(convID, args, message string, markers []domain.AnnouncedRecord)
	// now is the injected clock for deterministic recency checks.
	now func() time.Time
}

// runTaskMemorySemanticLane is the core async semantic lane logic. It:
//  1. Checks the breaker (skip if tripped).
//  2. Checks EffectiveType (conversation only).
//  3. Resolves the embedder (skip if nil — BM25 turn-start is enough).
//  4. Builds the query (title + workspace + last user prompt).
//  5. Checks recall intent on the last user message.
//  6. Runs a quick BM25 check: if no intent and BM25 found hits, skip
//     (fallback hemat — BM25 already covered it).
//  7. Runs a semantic (embedding) search with cache.
//  8. On error, records a breaker failure and returns.
//  9. On success, records a breaker success.
//
// 10. Filters results against recency + dedup.
// 11. If new hits remain, publishes a task_memory announcement.
func runTaskMemorySemanticLane(ctx context.Context, conv *domain.Conversation, d taskMemoryLaneDeps) {
	if conv == nil || conv.EffectiveType() != domain.ConversationTypeConversation {
		return
	}
	if d.breaker != nil && d.breaker.Tripped() {
		return
	}
	if d.resolveEmbed == nil || d.searcher == nil {
		return
	}
	embed, modelID := d.resolveEmbed()
	if embed == nil {
		return // BM25 turn-start already covers it
	}
	if d.now == nil {
		d.now = func() time.Time { return clock.NewTime().Time() }
	}

	// Build the query: title + workspace (TaskMemoryQuery) + last user prompt.
	query := memory.TaskMemoryQuery(conv)
	if prompt, ok := memory.LastUserPrompt(conv); ok {
		query = strings.TrimSpace(query + " " + prompt)
	}
	if strings.TrimSpace(query) == "" {
		return
	}

	// Intent gate: if the user shows recall intent, run the deeper semantic
	// search even when BM25 found hits. Without intent, only run when BM25
	// found nothing (fallback hemat).
	prompt, _ := memory.LastUserPrompt(conv)
	hasIntent := memory.HasRecallIntent(prompt)
	bm25Opts := SearchOptions{DisableEmbedding: true, MaxHops: 0}
	bm25Results, _ := d.searcher.SearchMemoryWithOpts(ctx, query, memory.TaskMemorySearchPool, bm25Opts)
	if !hasIntent && len(bm25Results) > 0 {
		return // BM25 already found it, no recall intent → skip
	}

	// Semantic search with embedding + cache.
	semOpts := SearchOptions{DisableEmbedding: false, EmbedCache: d.cache, ModelID: modelID, MaxHops: 0}
	results, err := d.searcher.SearchMemoryWithOpts(ctx, query, memory.TaskMemorySearchPool, semOpts)
	if err != nil {
		if d.breaker != nil {
			d.breaker.RecordFailure()
		}
		return
	}
	if d.breaker != nil {
		d.breaker.RecordSuccess()
	}
	if len(results) == 0 {
		return
	}

	// Convert learn.SearchResult → memory.MemorySearchResult for the filter.
	memResults := make([]memory.MemorySearchResult, len(results))
	for i, r := range results {
		memResults[i] = memory.MemorySearchResult{ID: r.ID, Score: r.Score}
	}

	// Filter against recency + dedup.
	hits := d.filter(conv, memResults, d.now)
	if len(hits) == 0 {
		return
	}

	// Publish the announcement with dedup markers so the turn-start scan
	// does not re-announce the same records on the next turn.
	args := memory.TaskMemoryArgs(hits)
	msg := "Relevant task memory for this conversation is new or updated. Read the snippets; retrieve full records with memory op=search or memory op=get."
	if d.publish != nil {
		d.publish(conv.ID, args, msg, laneMarkers(d.records, hits))
	}
}

// laneMarkers builds dedup markers (ID + LastConfirmedAt) for the filtered
// hits by looking up each record's LastConfirmed from the record store.
// This mirrors the marker-writing logic in MaybeAnnounceTaskMemory
// (application/memory/task.go) without duplicating the dedup/filter logic
// — the dedup decision itself is made inside PublishTaskMemoryAnnouncement
// against the fresh conversation state.
func laneMarkers(records memory.RecordStore, hits []memory.TaskMemoryHit) []domain.AnnouncedRecord {
	if len(hits) == 0 {
		return nil
	}
	markers := make([]domain.AnnouncedRecord, 0, len(hits))
	for _, hit := range hits {
		var confirmedAt time.Time
		if records != nil {
			if rec, err := records.Get(hit.ID); err == nil && rec != nil {
				confirmedAt = rec.LastConfirmed
			}
		}
		markers = append(markers, domain.AnnouncedRecord{
			ID:              hit.ID,
			LastConfirmedAt: confirmedAt,
		})
	}
	return markers
}

// prefetchTaskMemorySemantic kicks a background job that runs the async
// semantic lane. Called from finishTurn on the interactive (non-headless)
// path. The lane resolves the embedder, checks the breaker, and runs a
// cache-aware embedding search. Results are published via
// PublishTaskMemoryAnnouncement to the conversation's pending queue
// (idle-tolerant), together with dedup markers so the turn-start scan
// does not re-announce the same records.
func (a *App) prefetchTaskMemorySemantic(conv *domain.Conversation) {
	if a == nil || conv == nil {
		return
	}
	a.goSafe("task-memory-semantic", func() {
		ctx, cancel := context.WithTimeout(context.Background(), taskMemoryLaneTimeout)
		defer cancel()
		runTaskMemorySemanticLane(ctx, conv, a.taskMemoryLaneDeps(conv))
	})
}

// taskMemoryLaneDeps builds the deps for the async semantic lane from the
// App's lazy-initialized services.
func (a *App) taskMemoryLaneDeps(conv *domain.Conversation) taskMemoryLaneDeps {
	return taskMemoryLaneDeps{
		searcher:     a.learningSearch(),
		resolveEmbed: func() (Embedder, string) { return a.learnService().ResolveEmbedderPair() },
		cache:        a.EmbeddingCache,
		filter:       a.memoryService().FilterTaskMemoryResults,
		breaker:      a.taskMemoryBreakerInstance(),
		records:      a.MemoryRecords,
		publish: func(convID, args, message string, markers []domain.AnnouncedRecord) {
			a.agentService().PublishTaskMemoryAnnouncement(convID, args, message, markers)
		},
		now: clockNow,
	}
}

// taskMemoryBreakerInstance lazily creates the breaker on first use.
func (a *App) taskMemoryBreakerInstance() *taskMemoryBreaker {
	if a.taskMemoryBreaker != nil {
		return a.taskMemoryBreaker
	}
	// No lock needed: App is single-threaded during composition; the
	// breaker is first accessed from finishTurn's goSafe, which runs
	// after init. If a race occurs, the worst case is two breakers —
	// both start at zero, harmless.
	a.taskMemoryBreaker = newTaskMemoryBreaker()
	return a.taskMemoryBreaker
}
