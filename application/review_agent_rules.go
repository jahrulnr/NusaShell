package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/resources"
)

// reviewAgentRules is the AgentReview rule set: a virtual conversation
// (pre-injected transcript + primary memory as tool results), whitelisted
// and local tools, mutation tracking for the learning log, and the
// "Nothing to save" early exit. It deliberately has no retry, no
// persistence, and no events beyond memory/skill updates. See
// docs/decisions/003-agent-engine.md.
type reviewAgentRules struct {
	agent       *BackgroundReviewAgent
	adapter     ProviderContext
	model       string
	conv        *domain.Conversation
	start       int
	end         int
	convPath    string
	tools       []ToolDef
	base        []ChatMessage
	settings    domain.Settings
	promptC     *PromptCachePolicy
	ctx         context.Context
	mutations   []ReviewMutation
	learningIDs []string
}

func (p *reviewAgentRules) rules() AgentRules {
	return AgentRules{
		// No retry: a review failure is surfaced to the caller, which
		// records it in the trajectory log; the cooldown gate paces
		// retries.
		Stream: func(ctx context.Context, req ChatRequest) (ChatResponse, error) {
			var resp ChatResponse
			resp, err := p.adapter.Stream(ctx, req, func(delta string) {
				resp.Content += delta
			}, func(delta string) {
				resp.Reasoning += delta
			})
			if err != nil {
				return resp, err
			}
			if strings.TrimSpace(resp.Content) == "" && strings.TrimSpace(resp.Reasoning) == "" && len(resp.ToolCalls) == 0 {
				return resp, fmt.Errorf("empty response from review model")
			}
			return resp, nil
		},
		BuildRequest: func(st *RoundState) ChatRequest {
			messages := make([]ChatMessage, 0, len(p.base)+len(st.Messages))
			messages = append(messages, p.base...)
			messages = append(messages, st.Messages...)
			return ChatRequest{
				Model:          p.model,
				System:         resources.ReviewPrompt(),
				Messages:       messages,
				Tools:          p.tools,
				PromptCaching:  p.settings.PromptCaching,
				PromptCache:    p.promptC,
				ConversationID: p.conv.ID,
			}
		},
		// Terminal: the "Nothing to save." signal or a text-only response.
		// The empty-response case is rejected in Stream before this runs.
		Terminal: func(st *RoundState, resp ChatResponse) bool {
			return isNothingToSave(resp.Content) || len(resp.ToolCalls) == 0
		},
		// Execute runs each tool call with the review gates: local tools
		// (review_transcript, model_override) bypass the whitelist; the
		// remaining calls must pass reviewAllowedOp. Mutations and
		// learning-node usage are tracked here (side effects).
		Execute: func(st *RoundState, resp ChatResponse, calls []domain.ToolCall) ([]ToolOutcome, error) {
			out := make([]ToolOutcome, 0, len(calls))
			for _, tc := range calls {
				switch {
				case tc.Name == reviewTranscriptToolName:
					// Parse optional start/end from the agent's call args.
					// Default to the pre-injected range when omitted.
					ts, te := p.start, p.end
					var path string
					if len(tc.Args) > 0 {
						var args struct {
							MessagesStart *int   `json:"messages_start"`
							MessagesEnd   *int   `json:"messages_end"`
							Path          string `json:"path"`
						}
						if json.Unmarshal([]byte(tc.Args), &args) == nil {
							if args.MessagesStart != nil {
								ts = *args.MessagesStart
							}
							if args.MessagesEnd != nil {
								te = *args.MessagesEnd
							}
							path = args.Path
						}
					}
					if path == "" {
						path = p.convPath
					}
					out = append(out, ToolOutcome{
						Status: domain.ToolOK,
						Output: p.agent.executeReviewTranscript(p.conv, ts, te, path),
					})
				case tc.Name == modelOverrideToolName:
					output, snippet, _ := p.agent.executeModelOverride([]byte(tc.Args))
					if snippet != "" {
						p.mutations = append(p.mutations, ReviewMutation{
							Kind:    "model_override",
							Tool:    tc.Name,
							Snippet: snippet,
						})
						if p.agent.app.Bus != nil {
							p.agent.app.Bus.Emit(contracts.EventModelOverrideUpdated, map[string]any{
								"source": "review",
								"tool":   tc.Name,
							})
						}
					}
					out = append(out, ToolOutcome{Status: domain.ToolOK, Output: output})
				case !reviewAllowedOp(tc.Name, []byte(tc.Args)):
					out = append(out, ToolOutcome{
						Status: domain.ToolFailed,
						Output: fmt.Sprintf("error: tool %q (with this op) is not allowed in background review", tc.Name),
					})
				default:
					output, execErr := p.agent.app.Toolbox.Execute(p.ctx, tc.Name, []byte(tc.Args))
					if execErr != nil {
						output = "error: " + execErr.Error()
						// Only count a mutation when the tool actually
						// succeeded: recording it on failure produced
						// misleading trajectory entries ("saved") with
						// nothing persisted.
						p.agent.app.log("warn", "learning", "review tool %q failed: %v", tc.Name, execErr)
						out = append(out, ToolOutcome{Status: domain.ToolFailed, Output: output})
						continue
					}
					p.learningIDs = append(p.learningIDs, learningNodeIDsFromTool(p.agent.app, tc, output)...)
					// Track mutations with enough detail for the learning
					// log (which tool saved what, trimmed).
					op := OpArg([]byte(tc.Args))
					switch {
					case tc.Name == "memory" && (op == "save" || op == "replace"):
						p.mutations = append(p.mutations, ReviewMutation{Kind: "memory", Tool: tc.Name, Snippet: mutationSnippet(tc.Args, "content")})
					case tc.Name == "skill" && op == "save":
						p.mutations = append(p.mutations, ReviewMutation{Kind: "skills", Tool: tc.Name, Snippet: mutationSnippet(tc.Args, "name")})
					}
					// Emit learning mutation events so the Learning UI
					// refreshes memory/skill panes during a review.
					if p.agent.app.Bus != nil {
						switch {
						case tc.Name == "memory" && (op == "save" || op == "replace"):
							p.agent.app.Bus.Emit(contracts.EventMemoryUpdated, map[string]any{
								"source": "review",
								"tool":   tc.Name,
							})
						case tc.Name == "skill" && op == "save":
							p.agent.app.Bus.Emit(contracts.EventSkillUpdated, map[string]any{
								"source": "review",
								"tool":   tc.Name,
							})
						}
					}
					out = append(out, ToolOutcome{Status: domain.ToolOK, Output: output})
				}
			}
			return out, nil
		},
		// OnRound records the round in the returned history: the assistant
		// message with its tool calls, the tool results (zipped by call
		// order), and — on the terminal round — the conclusion message.
		OnRound: func(st *RoundState, resp ChatResponse, outcomes []ToolOutcome) error {
			if len(outcomes) == 0 {
				// Terminal round: persist the conclusion (the "Nothing to
				// save." phrase or the final text/reasoning).
				if strings.TrimSpace(resp.Content) != "" || strings.TrimSpace(resp.Reasoning) != "" {
					st.Messages = append(st.Messages, ChatMessage{
						Role:      "assistant",
						Content:   resp.Content,
						Reasoning: resp.Reasoning,
					})
				}
				return nil
			}
			st.Messages = append(st.Messages, ChatMessage{
				Role:      "assistant",
				Content:   resp.Content,
				Reasoning: resp.Reasoning,
				ToolCalls: resp.ToolCalls,
			})
			for i, tc := range resp.ToolCalls {
				if i >= len(outcomes) {
					break
				}
				st.Messages = append(st.Messages, ChatMessage{
					Role: "tool",
					ToolResult: &ToolResult{
						ToolCallID: tc.ID,
						Name:       tc.Name,
						Content:    outcomes[i].Output,
					},
				})
			}
			return nil
		},
	}
}
