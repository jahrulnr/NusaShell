// BackgroundReviewAgent runs a bounded LLM turn after a conversation crosses
// the learning-review threshold. It replays a transcript tail to the same
// provider that completed the parent turn ("global LLM"), with a restricted
// toolset (memory/skill dispatcher families, minus destructive verbs) and
// the review.md system prompt.
//
// This replaces the earlier regex-based ExtractObservations path, which was
// English-only and produced noisy one-off entries. The LLM understands any
// language and can judge what is genuinely durable.
//
// Design follows the learning-sources-audit Phase 2:
//   - Background review after N turns (fork agent, replay snapshot)
//   - LLM-based extraction (no regex)
//   - Restricted tool whitelist (memory + skill meta-tools only)
//   - Bounded tool rounds (maxToolRounds, default 6)
//   - No streaming; parent conversation is not modified
//   - Review transcript and mutation events are persisted for observability
//   - Mutations tracked for the Learning log
package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/resources"
)

// ReviewSettings controls the background review agent.
type ReviewSettings struct {
	Enabled             bool
	MemoryEveryNTurns   int           // trigger threshold (from settings.LearningReviewThreshold)
	MaxToolRounds       int           // bounded tool loop (default 6)
	TranscriptTailMsgs  int           // how many recent messages to include (default 40)
	MaxTranscriptChars  int           // per-message truncation (default 4000)
	MaxTranscriptTokens int           // total transcript cap, ~chars/4 (default 30000)
	ReviewCooldown      time.Duration // minimum interval per conversation (default 15m)
}

const defaultReviewCooldown = 15 * time.Minute

// DefaultReviewSettings returns sensible defaults.
func DefaultReviewSettings() ReviewSettings {
	return ReviewSettings{
		Enabled:             true,
		MemoryEveryNTurns:   10,
		MaxToolRounds:       6,
		TranscriptTailMsgs:  40,
		MaxTranscriptChars:  4000,
		MaxTranscriptTokens: 30000,
		ReviewCooldown:      defaultReviewCooldown,
	}
}

// ReviewMutation tracks what the review agent saved or updated.
type ReviewMutation struct {
	Kind    string // "memory" | "skills"
	Tool    string // tool name that produced the mutation
	Snippet string // trimmed content/name saved or updated, for the learning log
}

// BackgroundReviewAgent spawns a restricted LLM review turn.
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
	if settings.MaxToolRounds <= 0 {
		settings.MaxToolRounds = 6
	}
	if settings.TranscriptTailMsgs <= 0 {
		settings.TranscriptTailMsgs = 40
	}
	if settings.MaxTranscriptChars <= 0 {
		settings.MaxTranscriptChars = 4000
	}
	if settings.MaxTranscriptTokens <= 0 {
		settings.MaxTranscriptTokens = 30000
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

// reviewToolWhitelist is the only set of tools the review agent may call,
// derived from the dispatcher families (tool_dispatch.go) so new verbs are
// picked up automatically. Destructive verbs (delete/files) stay excluded.
func reviewToolWhitelist() map[string]bool {
	m := make(map[string]bool)
	for _, root := range []string{"memory", "skill"} {
		for _, member := range FamilyMembers(root) {
			if _, op, _ := MemberOp(member); op == "delete" || op == "files" {
				continue
			}
			m[member] = true
		}
	}
	return m
}

// review_transcript is the hydration tool — it returns the conversation as
// structured JSON. It is NOT registered in Toolbox; it is executed locally
// by runReviewLoop, not via Toolbox.Execute.

// reviewTranscriptToolName is the hydration tool the review agent calls to
// get the conversation transcript as structured JSON. It is NOT in Toolbox.
const reviewTranscriptToolName = "review_transcript"

// reviewTranscriptToolDef is the tool definition sent to the LLM so it knows
// review_transcript exists and how to call it.
var reviewTranscriptToolDef = ToolDef{
	Name:        reviewTranscriptToolName,
	Description: "Get the conversation transcript as structured JSON with proper roles, tool calls, and tool results. Call this FIRST to see what happened, then decide what to save.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tail": map[string]any{
				"type":        "integer",
				"description": "Number of recent messages to include. Omit or 0 for the full configured tail window.",
			},
		},
	},
}

// RunReview spawns a background review for a conversation. It uses the
// conversation's configured model (the "global LLM") and is fire-and-forget
// — it never blocks or fails the parent turn. Returns an error describing
// why the review aborted (missing conversation, no model, adapter failure,
// LLM error) so the caller can emit a toast and record it in the
// trajectory log.
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
	adapter, err := r.app.Factory(context.Background(), provider, apiKey)
	if err != nil {
		r.app.log("warn", "learning", "review aborted: cannot build adapter for %q: %v", model, err)
		return fmt.Errorf("cannot build adapter for %q: %v", model, err)
	}
	// Optional dedicated review model (settings.ReviewModel). Reviews re-send
	// the transcript tail, so routing them to a cheaper/faster model cuts
	// background cost. Falls back to the conversation model when unset or
	// unresolvable, mirroring resolveCompactionAdapter.
	adapter, bareModel = r.applyReviewModelOverride(context.Background(), adapter, bareModel)

	r.app.log("info", "learning", "review started: conv=%s model=%s rounds=%d", conversationID, bareModel, r.settings.MaxToolRounds)
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
	// but steadily streaming one. MaxToolRounds and ReviewCooldown bound the
	// total work independently of wall-clock time.
	mutations, messages, loopErr := r.runReviewLoop(ctx, adapter, bareModel, conversation)
	reviewID := saveReviewTranscript(r.app.DataDir, conversationID, model, messages)
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

// tryAcquireReview is retained for focused in-package callers/tests. It uses
// the same coalescing and retry-only cooldown policy as reserveReview.
func (r *BackgroundReviewAgent) tryAcquireReview(conversationID string) bool {
	reserved, _, _ := r.acquireReview(conversationID)
	return reserved
}

// releaseReview clears the active slot. Only failed reviews enter retry
// cooldown; successful reviews may process later evidence. It returns whether
// new activity was coalesced while the review was active.
func (r *BackgroundReviewAgent) releaseReview(conversationID string, failed ...bool) bool {
	r.reviewMu.Lock()
	defer r.reviewMu.Unlock()
	pending := r.pending[conversationID]
	delete(r.inFlight, conversationID)
	if len(failed) > 0 && failed[0] && r.settings.ReviewCooldown > 0 {
		r.lastReview[conversationID] = r.reviewNow()
	} else {
		delete(r.lastReview, conversationID)
	}
	return pending
}

func (r *BackgroundReviewAgent) reviewNow() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
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
func (r *BackgroundReviewAgent) applyReviewModelOverride(ctx context.Context, adapter AIProvider, model string) (AIProvider, string) {
	if r.app == nil || r.app.Settings == nil {
		return adapter, model
	}
	rm := strings.TrimSpace(r.app.Settings.Get().ReviewModel)
	if rm == "" {
		return adapter, model
	}
	rProvider, rBare, rKey, rErr := r.app.resolveModel(rm)
	if rErr != nil || rProvider == nil {
		r.app.log("warn", "learning", "review model %q could not be resolved, falling back to conversation model: %v", rm, rErr)
		r.recordReviewModelResolution(rm, "fallback:conv_model", model)
		return adapter, model
	}
	rAdapter, fErr := r.app.Factory(ctx, rProvider, rKey)
	if fErr != nil {
		r.app.log("warn", "learning", "review model %q adapter build failed, falling back to conversation model: %v", rm, fErr)
		r.recordReviewModelResolution(rm, "fallback:conv_model", model)
		return adapter, model
	}
	// Record the override only when it actually changes the model. When the
	// override resolves to the same model the conversation already uses, the
	// event carries no information — recording it every run turned the
	// learning log into repeated "ok" noise. Fallbacks are always recorded:
	// they mean the configured override did NOT apply.
	if rBare != model {
		r.app.log("info", "learning", "review using override model %s", rm)
		r.recordReviewModelResolution(rm, "ok", rBare)
	}
	return rAdapter, rBare
}

// recordReviewModelResolution writes a trajectory event recording whether
// the review model override fell back to the conversation model or applied
// a different one. Same-model overrides are not recorded: they are a no-op
// and would only repeat as "ok" noise in the learning log. This keeps
// override failures visible in the learning log instead of silently
// logging a warning that is easy to miss.
func (r *BackgroundReviewAgent) recordReviewModelResolution(requested, status, resolved string) {
	if r.app == nil || r.app.Trajectory == nil {
		return
	}
	r.app.Trajectory.Record("review_model", map[string]interface{}{
		"requested": requested,
		"status":    status,
		"resolved":  resolved,
	})
}

// mutationSnippet extracts a trimmed field value from a tool-call JSON
// args blob, capped so the trajectory log stays readable. The args may
// contain non-string fields (e.g. tags arrays), so the field is read as
// raw JSON and decoded as a string.
func mutationSnippet(argsJSON, field string) string {
	var args map[string]json.RawMessage
	if json.Unmarshal([]byte(argsJSON), &args) != nil {
		return ""
	}
	raw, ok := args[field]
	if !ok {
		return ""
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	v = strings.TrimSpace(v)
	const max = 200
	if len(v) > max {
		v = v[:max] + "…"
	}
	return v
}

// runReviewLoop executes the bounded tool loop: send transcript → get tool
// calls → execute whitelisted tools → feed results back → repeat. Returns
// the mutations and the full message history (the review agent's own
// conversation with the LLM) so it can be persisted for the learning log.
func (r *BackgroundReviewAgent) runReviewLoop(ctx context.Context, adapter AIProvider, model string, conversation *domain.Conversation) ([]ReviewMutation, []ChatMessage, error) {
	systemPrompt := resources.ReviewPrompt()
	if systemPrompt == "" {
		return nil, nil, nil
	}
	// Inject the current primary memory content into the system prompt so
	// the review agent can see what is already in primary.md before editing
	// it (avoid duplicates, spot stale text). Without this, the agent would
	// have to call memory_list target=primary first, burning a tool round
	// and sometimes skipping the check entirely.
	systemPrompt = r.injectPrimaryMemory(systemPrompt)
	// The review agent gets the conversation via the review_transcript
	// hydration tool, not as a flat user-message dump. The initial user
	// message instructs the LLM to call the tool first; the tool returns
	// structured JSON with proper roles, tool calls, and tool results.
	if len(conversation.Messages) == 0 {
		return nil, nil, nil
	}

	tools := r.reviewTools()
	messages := []ChatMessage{
		{Role: "user", Content: "Call review_transcript to see the conversation transcript, then decide what to save."},
	}

	var mutations []ReviewMutation
	for round := 0; round < r.settings.MaxToolRounds; round++ {
		if err := ctx.Err(); err != nil {
			return mutations, messages, err
		}
		req := ChatRequest{
			Model:    model,
			System:   systemPrompt,
			Messages: messages,
			Tools:    tools,
		}
		// Stream instead of a single non-streaming completion: deltas (text,
		// reasoning, tool-call fragments) keep arriving while the provider is
		// working, so a long-thinking reasoning model never hits a wall-clock
		// deadline. The adapter's ReadSSE layer enforces a per-chunk idle
		// timeout (resets on every delta); a hung stream surfaces as
		// KindIdleTimeout — a genuine failure, not "slow".
		var resp ChatResponse
		resp, err := adapter.Stream(ctx, req, func(delta string) {
			resp.Content += delta
		}, func(delta string) {
			resp.Reasoning += delta
		})
		if err != nil {
			return mutations, messages, err
		}
		// "Nothing to save." (case-insensitive, trimmed) is the prompt's
		// explicit signal that the review found no durable knowledge. The
		// LLM may add trailing punctuation or whitespace, so we check for
		// the phrase as a substring rather than exact equality.
		if strings.TrimSpace(resp.Content) == "" && strings.TrimSpace(resp.Reasoning) == "" && len(resp.ToolCalls) == 0 {
			return mutations, messages, fmt.Errorf("empty response from review model")
		}
		if isNothingToSave(resp.Content) {
			// Persist the terminal response so the learning log can show the
			// review's conclusion ("Nothing to save." or a short summary)
			// instead of ending the transcript on the last tool result.
			messages = append(messages, ChatMessage{
				Role:      "assistant",
				Content:   resp.Content,
				Reasoning: resp.Reasoning,
			})
			break
		}
		if len(resp.ToolCalls) == 0 {
			// Terminal text response without tool calls: persist it as the
			// review's conclusion.
			if strings.TrimSpace(resp.Content) != "" || strings.TrimSpace(resp.Reasoning) != "" {
				messages = append(messages, ChatMessage{
					Role:      "assistant",
					Content:   resp.Content,
					Reasoning: resp.Reasoning,
				})
			}
			break
		}
		// Append assistant message with tool calls (reasoning kept for the
		// learning log's thinking disclosure).
		messages = append(messages, ChatMessage{
			Role:      "assistant",
			Content:   resp.Content,
			Reasoning: resp.Reasoning,
			ToolCalls: resp.ToolCalls,
		})
		// Execute each tool call and append results.
		for _, tc := range resp.ToolCalls {
			if !reviewToolWhitelist()[tc.Name] {
				messages = append(messages, ChatMessage{
					Role: "tool",
					ToolResult: &ToolResult{
						ToolCallID: tc.ID,
						Name:       tc.Name,
						Content:    fmt.Sprintf("error: tool %q is not allowed in background review", tc.Name),
					},
				})
				continue
			}
			// The hydration tool is executed locally — it is NOT in
			// Toolbox. It returns the conversation as structured JSON.
			if tc.Name == reviewTranscriptToolName {
				output := r.executeReviewTranscript(conversation)
				messages = append(messages, ChatMessage{
					Role: "tool",
					ToolResult: &ToolResult{
						ToolCallID: tc.ID,
						Name:       tc.Name,
						Content:    output,
					},
				})
				continue
			}
			output, execErr := r.app.Toolbox.Execute(ctx, tc.Name, []byte(tc.Args))
			if execErr != nil {
				output = "error: " + execErr.Error()
				// Only count a mutation when the tool actually succeeded.
				// Recording it on failure produced misleading trajectory
				// entries ("saved") with nothing persisted, and hid the
				// real cause (e.g. the review prompt telling the model to
				// call a non-existent tool).
				r.app.log("warn", "learning", "review tool %q failed: %v", tc.Name, execErr)
			} else {
				// Track mutations with enough detail for the learning log
				// (which tool saved what, trimmed so the trajectory stays
				// readable).
				switch tc.Name {
				case "memory_save", "memory_replace":
					mutations = append(mutations, ReviewMutation{Kind: "memory", Tool: tc.Name, Snippet: mutationSnippet(tc.Args, "content")})
				case "skill_save":
					mutations = append(mutations, ReviewMutation{Kind: "skills", Tool: tc.Name, Snippet: mutationSnippet(tc.Args, "name")})
				}
				// Emit learning mutation events so the Learning UI can
				// refresh memory/skill panes in real time during a review.
				if r.app.Bus != nil {
					switch tc.Name {
					case "memory_save", "memory_replace":
						r.app.Bus.Emit(contracts.EventMemoryUpdated, map[string]any{
							"source": "review",
							"tool":   tc.Name,
						})
					case "skill_save":
						r.app.Bus.Emit(contracts.EventSkillUpdated, map[string]any{
							"source": "review",
							"tool":   tc.Name,
						})
					}
				}
			}
			messages = append(messages, ChatMessage{
				Role: "tool",
				ToolResult: &ToolResult{
					ToolCallID: tc.ID,
					Name:       tc.Name,
					Content:    output,
				},
			})
		}
	}
	return mutations, messages, nil
}

// isNothingToSave reports whether the LLM's response signals that the
// review found no durable knowledge worth saving. The review prompt
// instructs the agent to respond with exactly "Nothing to save." when
// there is nothing to persist, but models sometimes add trailing
// whitespace, punctuation, or a short explanation. We treat the response
// as a no-save signal when the canonical phrase appears (case-insensitive)
// and no tool calls were made.
func isNothingToSave(content string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(content)), "nothing to save")
}

// injectPrimaryMemory reads the current primary memory from the PrimaryStore
// and substitutes it into the review system prompt's {{primary_memory}}
// placeholder. Each entry is rendered as a bullet line so the agent can
// see what is already in primary memory. memory_replace target=primary
// (substring match), not IDs, so no ID prefix is needed. Returns the
// prompt unchanged when no PrimaryStore is configured or the document is empty.
func (r *BackgroundReviewAgent) injectPrimaryMemory(prompt string) string {
	if r.app == nil || r.app.Primary == nil {
		return strings.ReplaceAll(prompt, resources.PrimaryMemoryPlaceholder(), "(unavailable)")
	}
	mem := r.app.Primary.Load()
	if mem == nil || len(mem.Entries) == 0 {
		return strings.ReplaceAll(prompt, resources.PrimaryMemoryPlaceholder(), "(empty)")
	}
	lines := make([]string, 0, len(mem.Entries))
	for _, e := range mem.Entries {
		lines = append(lines, fmt.Sprintf("- %s", e.Content))
	}
	return resources.SubstitutePrimaryMemory(prompt, lines)
}

// reviewTools returns the restricted tool definitions for the review agent.
func (r *BackgroundReviewAgent) reviewTools() []ToolDef {
	// The hydration tool is always first in the list and is NOT in Toolbox.
	out := []ToolDef{reviewTranscriptToolDef}
	all := r.app.Toolbox.ListTools()
	for _, t := range all {
		if reviewToolWhitelist()[t.Name] && t.Name != reviewTranscriptToolName {
			out = append(out, ToolDef{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
	}
	return out
}

// buildTranscript extracts the recent messages from the conversation and
// formats them as a plain-text transcript for the review agent. Tool calls
// and their outputs are included inline so the review agent can see what
// tools were used and what they returned — this is essential for spotting
// skill-worthy patterns (e.g. a multi-step tool workflow the agent should
// remember) and for understanding the context around a memory-worthy fact.
// The transcript is bounded by both message count and total tokens: the tail
// is truncated per message, then the whole transcript is trimmed from the
// oldest line until it fits the token cap, so tool-heavy conversations
// (dozens of tool calls per turn) cannot overflow the review model's window.
func (r *BackgroundReviewAgent) buildTranscript(c *domain.Conversation) string {
	tail := r.transcriptMessages(c)
	if len(tail) > r.settings.TranscriptTailMsgs {
		tail = tail[len(tail)-r.settings.TranscriptTailMsgs:]
	}
	var lines []string
	for _, m := range tail {
		role := string(m.Role)
		content := m.Content
		if len(content) > r.settings.MaxTranscriptChars {
			content = content[:r.settings.MaxTranscriptChars] + "…[truncated]"
		}
		// Render tool calls and their outputs inline with the message so
		// the review agent sees the full tool interaction, not just the
		// surrounding text. Tool args and outputs are truncated to keep
		// the transcript within the token budget.
		var parts []string
		if strings.TrimSpace(content) != "" {
			parts = append(parts, content)
		}
		for _, tc := range m.ToolCalls {
			args := truncate(strings.TrimSpace(tc.Args), maxToolArgsChars)
			output := truncate(strings.TrimSpace(tc.Output), maxToolOutputChars)
			toolLine := fmt.Sprintf("→ tool: %s(%s)", tc.Name, args)
			if output != "" {
				toolLine += "\n  result: " + output
			}
			parts = append(parts, toolLine)
		}
		if len(parts) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("[%s] %s", role, strings.Join(parts, "\n")))
	}
	lines = r.trimTranscriptLines(lines)
	return strings.Join(lines, "\n\n")
}

// transcriptMessages returns the messages to review. Compaction strips tool
// calls and reasoning from retained messages (StripForRetention), so when the
// conversation has archived chunks the latest chunk — which preserves the
// full dropped window including tool calls — is prepended to restore the
// tool-call evidence the review needs to spot skill-worthy patterns. Falls
// back to the live messages alone when no store is configured or the chunk
// is unavailable.
func (r *BackgroundReviewAgent) transcriptMessages(c *domain.Conversation) []domain.Message {
	msgs := c.Messages
	if r.app == nil || r.app.Conversations == nil || c.ChunkCount <= 0 {
		return msgs
	}
	chunk, err := r.app.Conversations.GetChunk(c.ID, c.ChunkCount-1)
	if err != nil || len(chunk) == 0 {
		return msgs
	}
	merged := make([]domain.Message, 0, len(chunk)+len(msgs))
	merged = append(merged, chunk...)
	merged = append(merged, msgs...)
	return merged
}

// trimTranscriptLines drops the oldest lines until the transcript fits the
// configured token cap (~chars/4), keeping the most recent detail. The most
// recent line is always kept even when it alone exceeds the cap (best effort).
func (r *BackgroundReviewAgent) trimTranscriptLines(lines []string) []string {
	charCap := r.settings.MaxTranscriptTokens * 4
	total := 0
	start := 0
	for i, line := range lines {
		total += len(line)
		for total > charCap && start < i {
			total -= len(lines[start])
			start++
		}
	}
	if start == 0 {
		return lines
	}
	return lines[start:]
}

// transcriptMessageJSON is one message in the structured JSON transcript
// returned by the review_transcript hydration tool.
type transcriptMessageJSON struct {
	Role      string                   `json:"role"`
	Content   string                   `json:"content"`
	ToolCalls []transcriptToolCallJSON `json:"tool_calls,omitempty"`
}

// transcriptToolCallJSON is one tool call nested inside an assistant message.
type transcriptToolCallJSON struct {
	Name   string `json:"name"`
	Args   string `json:"args,omitempty"`
	Output string `json:"output,omitempty"`
}

// transcriptEnvelope is the top-level JSON object returned by the hydration
// tool. It carries conversation metadata plus the message array.
type transcriptEnvelope struct {
	ConversationID string                  `json:"conversation_id"`
	Model          string                  `json:"model,omitempty"`
	MessageCount   int                     `json:"message_count"`
	Messages       []transcriptMessageJSON `json:"messages"`
}

// executeReviewTranscript returns the conversation transcript as structured
// JSON for the review agent. It is the local handler for the
// review_transcript hydration tool — NOT dispatched via Toolbox.Execute.
//
// The JSON preserves role alternation (user/assistant), nests tool calls
// inside the assistant message that produced them, and truncates per-message
// content and per-tool-output to the same caps as buildTranscript. The total
// output is bounded by MaxTranscriptTokens (chars = tokens*4) so tool-heavy
// conversations cannot overflow the review model's context window.
func (r *BackgroundReviewAgent) executeReviewTranscript(c *domain.Conversation) string {
	tail := r.transcriptMessages(c)
	if len(tail) > r.settings.TranscriptTailMsgs {
		tail = tail[len(tail)-r.settings.TranscriptTailMsgs:]
	}

	env := transcriptEnvelope{
		ConversationID: c.ID,
		Model:          c.Model,
		MessageCount:   len(tail),
		Messages:       make([]transcriptMessageJSON, 0, len(tail)),
	}

	for _, m := range tail {
		content := m.Content
		if len(content) > r.settings.MaxTranscriptChars {
			content = content[:r.settings.MaxTranscriptChars] + "…[truncated]"
		}
		msg := transcriptMessageJSON{
			Role:    string(m.Role),
			Content: content,
		}
		for _, tc := range m.ToolCalls {
			args := truncate(strings.TrimSpace(tc.Args), maxToolArgsChars)
			output := truncate(strings.TrimSpace(tc.Output), maxToolOutputChars)
			msg.ToolCalls = append(msg.ToolCalls, transcriptToolCallJSON{
				Name:   tc.Name,
				Args:   args,
				Output: output,
			})
		}
		env.Messages = append(env.Messages, msg)
	}

	// Trim from the oldest message until the serialized JSON fits the
	// configured token cap (~chars/4). The most recent message is always
	// kept even when it alone exceeds the cap (best effort).
	charCap := r.settings.MaxTranscriptTokens * 4
	for len(env.Messages) > 1 {
		raw, _ := json.Marshal(env)
		if len(raw) <= charCap {
			break
		}
		env.Messages = env.Messages[1:]
		env.MessageCount = len(env.Messages)
	}

	raw, _ := json.Marshal(env)
	return string(raw)
}

const (
	maxToolArgsChars   = 500 // per tool-call args cap in the transcript
	maxToolOutputChars = 800 // per tool-result cap in the transcript
)
