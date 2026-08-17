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
	"fmt"
	"strings"
	"time"

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
	Kind string // "memory" | "skills"
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
	"memory_save":   true,
	"memory_search": true,
	"memory_list":   true,
	"skill_list":    true,
	"skill_search":  true,
	"skill_read":    true,
	"skill_save":    true,
}

// RunReview spawns a background review for a conversation. It uses the
// conversation's configured model (the "global LLM") and is fire-and-forget
// — it never blocks or fails the parent turn.
func (r *BackgroundReviewAgent) RunReview(ctx context.Context, conversationID string) {
	if !r.settings.Enabled || r.app == nil {
		return
	}
	conversation, err := r.app.Conversations.Get(conversationID)
	if err != nil {
		return
	}
	model := conversation.Model
	if model == "" {
		return
	}
	provider, bareModel, apiKey, rpcErr := r.app.resolveModel(model)
	if rpcErr != nil || provider == nil {
		return
	}
	adapter, err := r.app.Factory(context.Background(), provider, apiKey)
	if err != nil {
		return
	}

	reviewCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()

	mutations := r.runReviewLoop(reviewCtx, adapter, bareModel, conversation)
	if len(mutations) > 0 && r.app.Trajectory != nil {
		kinds := make([]string, 0, len(mutations))
		seen := map[string]bool{}
		for _, m := range mutations {
			if !seen[m.Kind] {
				kinds = append(kinds, m.Kind)
				seen[m.Kind] = true
			}
		}
		r.app.Trajectory.Record("review", map[string]interface{}{
			"conversation": conversationID,
			"mutations":    kinds,
		})
	}
}

// runReviewLoop executes the bounded tool loop: send transcript → get tool
// calls → execute whitelisted tools → feed results back → repeat.
func (r *BackgroundReviewAgent) runReviewLoop(ctx context.Context, adapter AIProvider, model string, conversation *domain.Conversation) []ReviewMutation {
	systemPrompt := resources.ReviewPrompt()
	if systemPrompt == "" {
		return nil
	}
	transcript := r.buildTranscript(conversation)
	if transcript == "" {
		return nil
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
		if resp.Content == "Nothing to save." || (resp.Content == "" && len(resp.ToolCalls) == 0) {
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
				// Track mutations.
				switch tc.Name {
				case "memory_save":
					mutations = append(mutations, ReviewMutation{Kind: "memory"})
				case "skill_save":
					mutations = append(mutations, ReviewMutation{Kind: "skills"})
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
	return mutations
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
// formats them as a plain-text transcript for the review agent.
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
		if strings.TrimSpace(content) == "" && len(m.ToolCalls) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("[%s] %s", role, content))
	}
	return strings.Join(lines, "\n\n")
}
