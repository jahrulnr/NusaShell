// BackgroundReviewAgent runs a bounded LLM turn after a conversation crosses
// the learning-review threshold. It replays a transcript tail to the same
// provider that completed the parent turn ("global LLM"), with a restricted
// toolset (memory_save, memory_search, memory_list, skill_list, skill_search,
// skill_read, skill_save) and the review.md system prompt.
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
//   - No streaming, no UI events, no conversation persistence
//   - Mutations tracked for observability
package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/resources"
)

// ReviewSettings controls the background review agent.
type ReviewSettings struct {
	Enabled            bool
	MemoryEveryNTurns  int // trigger threshold (from settings.LearningReviewThreshold)
	MaxToolRounds      int // bounded tool loop (default 6)
	TranscriptTailMsgs int // how many recent messages to include (default 40)
	MaxTranscriptChars int // per-message truncation (default 4000)
}

// DefaultReviewSettings returns sensible defaults.
func DefaultReviewSettings() ReviewSettings {
	return ReviewSettings{
		Enabled:            true,
		MemoryEveryNTurns:  10,
		MaxToolRounds:      6,
		TranscriptTailMsgs: 40,
		MaxTranscriptChars: 4000,
	}
}

// ReviewMutation tracks what the review agent saved.
type ReviewMutation struct {
	Kind    string // "memory" | "skills"
	Tool    string // tool name that produced the mutation
	Snippet string // trimmed content/name saved, for the learning log
}

// BackgroundReviewAgent spawns a restricted LLM review turn.
type BackgroundReviewAgent struct {
	app      *App
	settings ReviewSettings
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
	return &BackgroundReviewAgent{app: app, settings: settings}
}

// reviewToolWhitelist is the only set of tools the review agent may call.
var reviewToolWhitelist = map[string]bool{
	"memory_save":    true,
	"memory_replace": true,
	"memory_search":  true,
	"memory_list":    true,
	"memory_promote": true,
	"memory_demote":  true,
	"skill_list":     true,
	"skill_search":   true,
	"skill_read":     true,
	"skill_save":     true,
}

// RunReview spawns a background review for a conversation. It uses the
// conversation's configured model (the "global LLM") and is fire-and-forget
// — it never blocks or fails the parent turn. Returns an error describing
// why the review aborted (missing conversation, no model, adapter failure,
// LLM error) so the caller can emit a toast and record it in the
// trajectory log.
func (r *BackgroundReviewAgent) RunReview(ctx context.Context, conversationID string) error {
	if !r.settings.Enabled || r.app == nil {
		r.app.log("debug", "learning", "review skipped: disabled or no app (conv=%s)", conversationID)
		return nil
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

	r.app.log("info", "learning", "review started: conv=%s model=%s rounds=%d", conversationID, bareModel, r.settings.MaxToolRounds)
	reviewCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()

	mutations, messages := r.runReviewLoop(reviewCtx, adapter, bareModel, conversation)
	reviewID := saveReviewTranscript(r.app.DataDir, conversationID, model, messages)
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
func (r *BackgroundReviewAgent) runReviewLoop(ctx context.Context, adapter AIProvider, model string, conversation *domain.Conversation) ([]ReviewMutation, []ChatMessage) {
	systemPrompt := resources.ReviewPrompt()
	if systemPrompt == "" {
		return nil, nil
	}
	// Inject the current primary memory content into the system prompt so
	// the review agent can see what is already in primary.md before deciding
	// to promote (avoid duplicates) or demote (spot stale entries). Without
	// this, the agent would have to call memory_list target=primary first,
	// burning a tool round and sometimes skipping the check entirely.
	systemPrompt = r.injectPrimaryMemory(systemPrompt)
	transcript := r.buildTranscript(conversation)
	if transcript == "" {
		return nil, nil
	}

	tools := r.reviewTools()
	messages := []ChatMessage{
		{Role: "user", Content: transcript},
	}

	var mutations []ReviewMutation
	for round := 0; round < r.settings.MaxToolRounds; round++ {
		if ctx.Err() != nil {
			break
		}
		req := ChatRequest{
			Model:    model,
			System:   systemPrompt,
			Messages: messages,
			Tools:    tools,
		}
		resp, err := adapter.Complete(ctx, req)
		if err != nil {
			break
		}
		// "Nothing to save." (case-insensitive, trimmed) is the prompt's
		// explicit signal that the review found no durable knowledge. The
		// LLM may add trailing punctuation or whitespace, so we check for
		// the phrase as a substring rather than exact equality.
		if isNothingToSave(resp.Content) || (resp.Content == "" && len(resp.ToolCalls) == 0) {
			break
		}
		if len(resp.ToolCalls) == 0 {
			break
		}
		// Append assistant message with tool calls.
		messages = append(messages, ChatMessage{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})
		// Execute each tool call and append results.
		for _, tc := range resp.ToolCalls {
			if !reviewToolWhitelist[tc.Name] {
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
				case "memory_save":
					mutations = append(mutations, ReviewMutation{Kind: "memory", Tool: tc.Name, Snippet: mutationSnippet(tc.Args, "content")})
				case "skill_save":
					mutations = append(mutations, ReviewMutation{Kind: "skills", Tool: tc.Name, Snippet: mutationSnippet(tc.Args, "name")})
				}
				// Emit learning mutation events so the Learning UI can
				// refresh memory/skill panes in real time during a review.
				if r.app.Bus != nil {
					switch tc.Name {
					case "memory_save", "memory_replace", "memory_promote", "memory_demote":
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
	return mutations, messages
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
// placeholder. Each entry is rendered as "- [id] content" so the agent can
// reference ids when calling memory_demote. Returns the prompt unchanged
// when no PrimaryStore is configured or the document is empty.
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
		lines = append(lines, fmt.Sprintf("- [%s] %s", e.ID, e.Content))
	}
	return resources.SubstitutePrimaryMemory(prompt, lines)
}

// reviewTools returns the restricted tool definitions for the review agent.
func (r *BackgroundReviewAgent) reviewTools() []ToolDef {
	all := r.app.Toolbox.ListTools()
	var out []ToolDef
	for _, t := range all {
		if reviewToolWhitelist[t.Name] {
			out = append(out, ToolDef{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
	}
	return out
}

// buildTranscript extracts the last N messages from the conversation and
// formats them as a plain-text transcript for the review agent. Tool calls
// and their outputs are included inline so the review agent can see what
// tools were used and what they returned — this is essential for spotting
// skill-worthy patterns (e.g. a multi-step tool workflow the agent should
// remember) and for understanding the context around a memory-worthy fact.
func (r *BackgroundReviewAgent) buildTranscript(c *domain.Conversation) string {
	msgs := c.Messages
	tail := msgs
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
	return strings.Join(lines, "\n\n")
}

const (
	maxToolArgsChars   = 500 // per tool-call args cap in the transcript
	maxToolOutputChars = 800 // per tool-result cap in the transcript
)
