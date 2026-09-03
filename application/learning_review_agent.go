// BackgroundReviewAgent is the unified post-conversation learning agent.
// It runs an agentic, tool-using pass over completed conversation evidence,
// then curates durable memory and agent-owned skills.
//
// This replaces the earlier regex-based ExtractObservations path, which was
// English-only and produced noisy one-off entries. The LLM understands any
// language and can judge what is genuinely durable.
//
// Design follows the learning-sources-audit Phase 2:
//   - Background learning after N turns (fork agent, replay snapshot)
//   - LLM-based extraction (no regex)
//   - Curation toolset: memory/skill dispatchers + evidence/research tools
//   - The shared AgentEngine loop with no review-specific round cap; it ends
//     when the model is terminal or an error is returned
//   - Streams responses without exposing a foreground turn; parent conversation is not modified
//   - Review transcript and mutation events are persisted for observability
//   - Mutations tracked for the Learning log
package application

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// ReviewSettings controls the unified background learning agent (kept
// wire-compatible with the learning.review surface).
type ReviewSettings struct {
	Enabled             bool
	MemoryEveryNTurns   int           // trigger threshold (from settings.LearningReviewThreshold)
	TranscriptTailMsgs  int           // how many recent messages to include (default 40)
	MaxTranscriptChars  int           // per-message truncation (default 4000)
	MaxTranscriptTokens int           // total transcript cap, ~chars/4 (default 30000)
	ReviewCooldown      time.Duration // minimum interval per conversation (default 15m)
}

const defaultReviewCooldown = domain.DefaultReviewCooldown

// DefaultReviewSettings returns sensible defaults.
func DefaultReviewSettings() ReviewSettings {
	return ReviewSettings{
		Enabled:             true,
		MemoryEveryNTurns:   domain.DefaultReviewMemoryEveryNTurns,
		TranscriptTailMsgs:  domain.DefaultReviewTranscriptTailMsgs,
		MaxTranscriptChars:  domain.DefaultReviewMaxTranscriptChars,
		MaxTranscriptTokens: domain.DefaultReviewMaxTranscriptTokens,
		ReviewCooldown:      defaultReviewCooldown,
	}
}

// ReviewMutation tracks what the review agent saved or updated.
type ReviewMutation struct {
	Kind    string // "memory" | "skills"
	Tool    string // tool name that produced the mutation
	Snippet string // trimmed content/name saved or updated, for the learning log
}

// BackgroundReviewAgent is the unified post-conversation learning agent.
// It runs an agentic, tool-using pass over completed conversation evidence,
// then curates durable memory and agent-owned skills.
type BackgroundReviewAgent struct {
	app      *App
	settings ReviewSettings

	// reviewMu prevents threshold and compaction triggers from launching
	// duplicate reviews for the same conversation. lastReview is a retry
	// backoff timestamp for failed reviews only; successful reviews do not
	// suppress later evidence.
	reviewMu     sync.Mutex
	inFlight     map[string]bool
	lastReview   map[string]time.Time
	pending      map[string]bool
	lastSkipByID map[string]string
	now          func() time.Time // injectable clock for deterministic tests
}

// NewBackgroundReviewAgent creates a review agent bound to the App.
func NewBackgroundReviewAgent(app *App, settings ReviewSettings) *BackgroundReviewAgent {
	if settings.TranscriptTailMsgs <= 0 {
		settings.TranscriptTailMsgs = domain.DefaultReviewTranscriptTailMsgs
	}
	if settings.MaxTranscriptChars <= 0 {
		settings.MaxTranscriptChars = domain.DefaultReviewMaxTranscriptChars
	}
	if settings.MaxTranscriptTokens <= 0 {
		settings.MaxTranscriptTokens = domain.DefaultReviewMaxTranscriptTokens
	}
	if settings.ReviewCooldown <= 0 {
		settings.ReviewCooldown = defaultReviewCooldown
	}
	return &BackgroundReviewAgent{
		app:          app,
		settings:     settings,
		inFlight:     make(map[string]bool),
		lastReview:   make(map[string]time.Time),
		pending:      make(map[string]bool),
		lastSkipByID: make(map[string]string),
	}
}

// backgroundLearningToolWhitelist is the explicit capability boundary for
// the unified post-conversation agent. It may inspect evidence, research,
// and curate memory/skills, but it cannot execute commands, write arbitrary
// files, run automations, or enter interactive/recursive agent flows.
func backgroundLearningToolWhitelist() map[string]bool {
	return map[string]bool{
		"memory":         true,
		"skill":          true,
		"file_read":      true,
		"file_list":      true,
		"file_info":      true,
		"find_file":      true,
		"grep":           true,
		"memory_project": true,
		"web_search":     true,
		"web_fetch":      true,
		"web_answer":     true,
		"docs":           true,
	}
}

// reviewToolWhitelist is retained as a source-compatible name for tests and
// callers inside the application package. New code should use the explicit
// backgroundLearningToolWhitelist name.
func reviewToolWhitelist() map[string]bool {
	return backgroundLearningToolWhitelist()
}

// reviewAllowedOp reports whether a unified background-learning call is
// allowed. Memory, skill, docs, and project-memory dispatcher operations
// are restricted to their curation/read surfaces; inspection and research
// tools have no dispatcher op.
func reviewAllowedOp(name string, argsJSON []byte) bool {
	if !backgroundLearningToolWhitelist()[name] {
		return false
	}
	switch name {
	case "memory":
		op := OpArg(argsJSON)
		return op == "save" || op == "replace" || op == "search" || op == "list" || op == "delete"
	case "skill":
		op := OpArg(argsJSON)
		return op == "list" || op == "search" || op == "save" || op == "delete"
	case "docs":
		op := OpArg(argsJSON)
		return op == "list" || op == "search" || op == "read"
	case "memory_project":
		op := OpArg(argsJSON)
		return op == "query" || op == "list" || op == "read"
	}
	return true
}

// review_transcript is the hydration tool — it returns the conversation as
// structured JSON. It is NOT registered in Toolbox; it is executed locally
// by runReviewLoop, not via Toolbox.Execute.

// reviewTranscriptToolName is the hydration tool the review agent calls to
// get the conversation transcript as structured JSON. It is NOT in Toolbox.
const reviewTranscriptToolName = "review_transcript"

// reviewTranscriptToolDef is the tool definition sent to the LLM so it
// knows review_transcript exists and how to call it. The path, start, and
// end params are informational — the review loop pre-injects the result
// before the first LLM call, so the agent does not need to call it. But
// the definition is kept so the agent understands the tool result shape
// and can optionally call it again for a different range if needed.
var reviewTranscriptToolDef = ToolDef{
	Name:        reviewTranscriptToolName,
	Description: "Get a bounded segment of the conversation transcript as structured JSON. The path field is the absolute path to the full conversation JSON file — use file_read on it if you need more context than the provided segment.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the full conversation JSON file. Use file_read on this path if the provided segment lacks context you need.",
			},
			"messages_start": map[string]any{
				"type":        "integer",
				"description": "Start message index (inclusive) of the segment to review. Omit to use the pre-injected segment.",
			},
			"messages_end": map[string]any{
				"type":        "integer",
				"description": "End message index (exclusive) of the segment to review. Omit to use the pre-injected segment.",
			},
		},
	},
}

// RunReview spawns the unified background learning pass for a conversation.
// It uses the
// conversation's configured model (the "global LLM") and is fire-and-forget
// — it never blocks or fails the parent turn. Returns an error describing
// why the review aborted (missing conversation, no model, adapter failure,
// or LLM error) so the background caller can record it in the trajectory log.
func (r *BackgroundReviewAgent) RunReview(ctx context.Context, conversationID string) error {
	if !r.reserveReview(conversationID) {
		return nil
	}
	return r.runReservedReview(ctx, conversationID)
}

// reserveReview claims a review slot before any lifecycle event is emitted.
// The caller must invoke runReservedReview after a successful reservation.
func (r *BackgroundReviewAgent) reserveReview(conversationID string) bool {
	reserved, _ := r.reserveReviewWithReason(conversationID)
	return reserved
}

// reserveReviewWithReason reserves one review slot and classifies rejected
// triggers. Repeated triggers in the same rejected state are coalesced into a
// single trajectory event, so a burst of turns does not flood the Learning log.
func (r *BackgroundReviewAgent) reserveReviewWithReason(conversationID string) (bool, string) {
	if !r.settings.Enabled || r.app == nil {
		if r.app != nil {
			r.app.log("debug", "learning", "review skipped: disabled or no app (conv=%s)", conversationID)
		}
		return false, "disabled"
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return false, "invalid_conversation"
	}

	reserved, reason, shouldRecord := r.acquireReview(conversationID)
	if reserved {
		return true, "reserved"
	}
	if shouldRecord {
		r.app.log("info", "learning", "review deferred: reason=%s (conv=%s)", reason, conversationID)
		r.recordReviewSkipped(conversationID, reason)
	}
	return false, reason
}

// runReservedReview executes a review after reserveReview has claimed its
// conversation slot. Keeping acquisition separate lets the threshold and
// compaction trigger emit "started" only for work that will actually run.
func (r *BackgroundReviewAgent) runReservedReview(ctx context.Context, conversationID string) (runErr error) {
	defer func() {
		if pending := r.releaseReview(conversationID, runErr != nil); pending && runErr == nil && r.app != nil {
			// New activity arrived while this review was running. Coalesce it
			// into one follow-up review instead of emitting more skip events.
			r.app.flushLearningReview(conversationID, "coalesced")
		}
	}()

	if r.app.Conversations == nil {
		return fmt.Errorf("conversation store not configured")
	}
	conversation, err := r.app.Conversations.Get(conversationID)
	if err != nil {
		r.app.log("warn", "learning", "review aborted: conversation %s not found: %v", conversationID, err)
		return fmt.Errorf("conversation not found: %w", err)
	}
	model := conversation.Model
	if model == "" {
		r.app.log("warn", "learning", "review aborted: conversation %s has no model configured", conversationID)
		return fmt.Errorf("no model configured")
	}
	provider, bareModel, apiKey, rpcErr := r.app.resolveModel(model)
	if rpcErr != nil || provider == nil {
		r.app.log("warn", "learning", "review aborted: cannot resolve model %q: %v", model, rpcErr)
		return fmt.Errorf("cannot resolve model %q: %v", model, rpcErr)
	}
	rawAdapter, err := r.app.Factory(context.Background(), provider, apiKey)
	if err != nil {
		r.app.log("warn", "learning", "review aborted: cannot build adapter for %q: %v", model, err)
		return fmt.Errorf("cannot build adapter for %q: %v", model, err)
	}
	adapter := NewProviderContext(provider, rawAdapter)
	// Optional dedicated review model (settings.ReviewModel). Reviews re-send
	// the transcript tail, so routing them to a cheaper/faster model cuts
	// background cost. Falls back to the conversation model when unset or
	// unresolvable, mirroring resolveCompactionAdapter.
	adapter, bareModel = r.applyReviewModelOverride(context.Background(), adapter, bareModel)

	r.app.log("info", "learning", "review started: conv=%s model=%s", conversationID, bareModel)
	if r.app.Trajectory != nil {
		r.app.Trajectory.Record("review", map[string]interface{}{
			"conversation": conversationID,
			"status":       "started",
			"model":        bareModel,
		})
	}
	// No wall-clock cap: reviews of long conversations (big transcripts, tool
	// rounds, reasoning models) can take several minutes. "Slow" is not an
	// error. The only activity-based guard is the provider's per-chunk idle
	// timeout (ReadSSE DefaultIdleTimeout) — it fires only when the stream
	// sends nothing for the idle window (a hung provider), never for a slow
	// but steadily streaming one. ReviewCooldown controls trigger frequency;
	// it is not an agent-loop limit.
	mutations, messages, loopErr := r.runReviewLoop(ctx, adapter, bareModel, conversation)
	reviewID := saveReviewTranscript(r.app.DataDir, conversationID, bareModel, messages)
	// Update the incremental review marker regardless of success or
	// failure. On success this prevents the next review from re-reading
	// the same segment. On failure this prevents a retry loop from
	// re-reading the same segment repeatedly — the cooldown gate handles
	// retry pacing. Only update when the loop actually processed messages
	// (runReviewLoop returns nil messages when there is nothing new).
	if messages != nil && r.app.Conversations != nil {
		newMarker := len(r.transcriptMessages(conversation))
		if newMarker > conversation.LastReviewedMsgCount {
			// Re-fetch the latest conversation state before saving the
			// marker. The review loop may have taken minutes, during which
			// the turn goroutine added messages and tool results. Saving
			// the stale snapshot fetched at the start of the review would
			// overwrite that progress — the "disappearing turn" race.
			latestRepo, err := r.app.loadRepo(conversationID)
			if err != nil {
				r.app.log("warn", "learning", "failed to re-fetch conversation for review marker: conv=%s err=%v", conversationID, err)
			} else {
				latest := latestRepo.Conversation()
				if newMarker > latest.LastReviewedMsgCount {
					latest.LastReviewedMsgCount = newMarker
					if err := latestRepo.Save(); err != nil {
						r.app.log("warn", "learning", "failed to persist review marker: conv=%s err=%v", conversationID, err)
					}
				}
			}
		}
	}
	if loopErr != nil {
		r.app.log("warn", "learning", "review failed mid-loop: conv=%s err=%v mutations=%d", conversationID, loopErr, len(mutations))
		return loopErr
	}
	if r.app.Trajectory != nil {
		// Record the review even when nothing was saved: the learning log
		// shows when reviews ran and concluded with no mutations, which is
		// useful signal ("reviewed, nothing durable").
		muts := make([]map[string]string, 0, len(mutations))
		for _, m := range mutations {
			muts = append(muts, map[string]string{
				"kind":    m.Kind,
				"tool":    m.Tool,
				"snippet": m.Snippet,
			})
		}
		r.app.Trajectory.Record("review", map[string]interface{}{
			"conversation": conversationID,
			"review_id":    reviewID,
			"status":       "done",
			"mutations":    muts,
		})
	}
	r.app.log("info", "learning", "review done: conv=%s mutations=%d review_id=%s", conversationID, len(mutations), reviewID)
	return nil
}

// acquireReview is the in-memory reservation gate. It returns whether the
// caller won, the rejection reason, and whether this reason has not already
// been recorded for the conversation (coalescing repeated triggers).
func (r *BackgroundReviewAgent) acquireReview(conversationID string) (bool, string, bool) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return false, "invalid_conversation", false
	}
	r.reviewMu.Lock()
	defer r.reviewMu.Unlock()
	if r.inFlight == nil {
		r.inFlight = make(map[string]bool)
	}
	if r.lastReview == nil {
		r.lastReview = make(map[string]time.Time)
	}
	if r.pending == nil {
		r.pending = make(map[string]bool)
	}
	if r.lastSkipByID == nil {
		r.lastSkipByID = make(map[string]string)
	}
	if r.inFlight[conversationID] {
		r.pending[conversationID] = true
		first := r.lastSkipByID[conversationID] != "already_running"
		r.lastSkipByID[conversationID] = "already_running"
		return false, "already_running", first
	}
	if last, ok := r.lastReview[conversationID]; ok && r.reviewNow().Before(last.Add(r.settings.ReviewCooldown)) {
		r.pending[conversationID] = true
		first := r.lastSkipByID[conversationID] != "cooldown_active"
		r.lastSkipByID[conversationID] = "cooldown_active"
		return false, "cooldown_active", first
	}
	r.inFlight[conversationID] = true
	r.pending[conversationID] = false
	delete(r.lastSkipByID, conversationID)
	return true, "reserved", false
}

// releaseReview clears the active slot. Both successful and failed reviews
// enter the cooldown — this prevents the threshold trigger from launching a
// redundant review immediately after a completed one (the incremental marker
// prevents re-reading, but the cooldown prevents wasted reservation attempts
// during the burst of turns that cross the threshold). It returns whether
// new activity was coalesced while the review was active.
//
// When activity was coalesced (pending) AND the review succeeded, the
// cooldown is skipped so the coalesced follow-up review in
// runReservedReview's defer can reserve immediately. Setting cooldown here
// would block that follow-up at the very gate this release just opened,
// leaving the coalesced activity unreviewed. Failed reviews always enter the
// cooldown regardless of pending, so a failed review is not retried
// immediately. The follow-up's own release reapplies the cooldown.
func (r *BackgroundReviewAgent) releaseReview(conversationID string, failed ...bool) bool {
	r.reviewMu.Lock()
	defer r.reviewMu.Unlock()
	pending := r.pending[conversationID]
	delete(r.inFlight, conversationID)
	isFailed := len(failed) > 0 && failed[0]
	if r.settings.ReviewCooldown > 0 && !(pending && !isFailed) {
		r.lastReview[conversationID] = r.reviewNow()
	} else if r.settings.ReviewCooldown <= 0 {
		delete(r.lastReview, conversationID)
	}
	return pending
}

func (r *BackgroundReviewAgent) reviewNow() time.Time {
	if r.now != nil {
		return clock.NewTime(r.now()).Time()
	}
	return clock.NewTime().Time()
}

// recordReviewSkipped keeps coalesced/deferred decisions visible in the
// learning log without pretending a new LLM review completed or creating a
// transcript.
func (r *BackgroundReviewAgent) recordReviewSkipped(conversationID string, reason ...string) {
	if r.app == nil || r.app.Trajectory == nil {
		return
	}
	reasonValue := "cooldown_active"
	if len(reason) > 0 && strings.TrimSpace(reason[0]) != "" {
		reasonValue = reason[0]
	}
	r.app.Trajectory.Record("review", map[string]interface{}{
		"conversation": conversationID,
		"status":       "skipped",
		"reason":       reasonValue,
		"coalesced":    true,
	})
}

// recordReviewError writes an error trajectory entry so the learning log
// shows failed reviews with their error message. Called by the caller
// when RunReview returns a non-nil error.
func (r *BackgroundReviewAgent) recordReviewError(conversationID, errMsg string) {
	if r.app == nil || r.app.Trajectory == nil {
		return
	}
	r.app.Trajectory.Record("review", map[string]interface{}{
		"conversation": conversationID,
		"status":       "error",
		"error":        errMsg,
	})
}

// applyReviewModelOverride routes the review to settings.ReviewModel when
// configured and resolvable; otherwise it returns the given adapter+model
// unchanged so reviews still run. `model` is the bare model id used for
// logging, not the "providerID:modelID" form.

const (
	maxToolArgsChars   = domain.ReviewMaxToolArgsChars   // per tool-call args cap in the transcript
	maxToolOutputChars = domain.ReviewMaxToolOutputChars // per tool-result cap in the transcript
)
