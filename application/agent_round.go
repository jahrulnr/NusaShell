package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

type streamedTurnRound struct {
	Content   string
	Reasoning string
	Response  ChatResponse
}

func (a *App) initializeTurn(run *TurnRun, provider *domain.Provider, apiKey, model string) (AIProvider, *domain.Conversation, domain.Settings, error) {
	adapter, err := a.Factory(run.Ctx, provider, apiKey)
	if err != nil {
		return nil, nil, domain.Settings{}, err
	}

	conversation, err := a.Conversations.Get(run.ConversationID)
	if err != nil {
		return nil, nil, domain.Settings{}, err
	}
	settings := a.Settings.Get()
	contextWindow := resolveContextWindow(provider, model, settings)
	compactionTrigger := compactionTriggerTokens(contextWindow, settings)
	if !settings.CompactionEnabled || conversation.EstimateTokens() <= compactionTrigger {
		return adapter, conversation, settings, nil
	}

	summary, err := a.compactConversation(run.Ctx, adapter, conversation, model, contextWindow)
	if err != nil {
		a.log("warn", "agent", "compaction failed for %s: %v", conversation.ID, err)
	} else {
		a.Bus.Emit(contracts.EventCompacted, contracts.CompactedEvent{ConversationID: conversation.ID, Summary: summary})
		a.log("info", "agent", "compacted conversation %s", conversation.ID)
	}
	conversation, err = a.Conversations.Get(run.ConversationID)
	return adapter, conversation, settings, err
}

func (a *App) toolDefinitions() []ToolDef {
	tools := a.Toolbox.ListTools()
	definitions := make([]ToolDef, 0, len(tools))
	for _, tool := range tools {
		definitions = append(definitions, ToolDef{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}
	return definitions
}

func (a *App) streamTurnRound(run *TurnRun, adapter AIProvider, conversation *domain.Conversation, messageID, model, effort string, tools []ToolDef, settings domain.Settings, continuation bool, maxTokens int, injectHydration bool, promptCache *PromptCachePolicy) (streamedTurnRound, error) {
	for retry := 1; ; retry++ {
		roundResult, err := a.streamTurnRoundOnce(run, adapter, conversation, messageID, model, effort, tools, settings, continuation, maxTokens, injectHydration, promptCache)
		if err == nil || retry >= maxProviderAttempts || roundResult.Content != "" || roundResult.Reasoning != "" {
			return roundResult, err
		}
		delay, retryable := providerRetryDelay(err, retry)
		if !retryable {
			return roundResult, err
		}
		a.log("warn", "ai", "retrying provider stream for turn %s (%d/%d) after %s: %s", run.ID, retry, maxProviderAttempts, delay.Round(time.Millisecond), describeProviderError(err))
		retryEvt := contracts.ProviderRetryEvent{
			RunID:          run.ID,
			ConversationID: run.ConversationID,
			MessageID:      messageID,
			Attempt:        retry + 1,
			MaxAttempts:    maxProviderAttempts,
			DelayMS:        delay.Milliseconds(),
			Error:          err.Error(),
		}
		var upstream *UpstreamError
		if errors.As(err, &upstream) {
			retryEvt.Kind = string(upstream.Kind)
			retryEvt.Status = upstream.StatusCode
		}
		a.Bus.Emit(contracts.EventProviderRetry, retryEvt)
		if err := a.retrySleeper(run.Ctx, delay); err != nil {
			return roundResult, err
		}
	}
}

func (a *App) streamTurnRoundOnce(run *TurnRun, adapter AIProvider, conversation *domain.Conversation, messageID, model, effort string, tools []ToolDef, settings domain.Settings, continuation bool, maxTokens int, injectHydration bool, promptCache *PromptCachePolicy) (streamedTurnRound, error) {
	var content strings.Builder
	var reasoning strings.Builder
	system := buildSystemPrompt(conversation)
	if continuation {
		system += "\n\nThe immediately preceding assistant response was interrupted by a transient upstream failure. Continue it from exactly where it stopped. Do not repeat prior text."
	}
	messages := chatMessages(conversation, messageID)
	// Inject the synthetic runtime-hydration transcript (runtime_context,
	// memory, skill_list, mcp_list, tool_list) as an ephemeral assistant
	// toolCalls + tool results exchange, placed AFTER the durable history and
	// BEFORE the model's own output. This gives the model fresh runtime facts
	// (date, workspace, memory, skills, MCP catalog, tool catalog) without
	// baking volatile values into the stable system prompt prefix (which would
	// break prompt-cache hits). The transcript is never persisted in the
	// conversation store.
	//
	// Hydration is injected exactly once per user message: on the first round
	// of a turn (after the initial user message or post-compaction), and once
	// more when a steer is applied (a new user message mid-turn). Re-injecting
	// every tool round causes smaller models to misinterpret the synthetic
	// tool calls as a pattern to repeat ("call all tools in parallel every
	// round"), leading to redundant parallel tool calls.
	if injectHydration {
		messages = append(messages, a.buildHydration(conversation)...)
	}
	response, err := adapter.Stream(run.Ctx, ChatRequest{
		Model:            model,
		System:           system,
		Messages:         messages,
		Tools:            tools,
		PromptCaching:    settings.PromptCaching,
		PromptCache:      promptCache,
		MaxTokens:        maxTokens,
		Effort:           effort,
		Temperature:      settings.Temperature,
		TopP:             settings.TopP,
		TopK:             settings.TopK,
		FrequencyPenalty: settings.FrequencyPenalty,
		PresencePenalty:  settings.PresencePenalty,
		ConversationID:   run.ConversationID,
	}, func(delta string) {
		content.WriteString(delta)
		a.Bus.Emit(contracts.EventMessageDelta, contracts.MessageDeltaEvent{
			RunID: run.ID, ConversationID: run.ConversationID, MessageID: messageID, Text: delta,
		})
	}, func(delta string) {
		reasoning.WriteString(delta)
		a.Bus.Emit(contracts.EventReasoningDelta, contracts.ReasoningDeltaEvent{
			RunID: run.ID, ConversationID: run.ConversationID, MessageID: messageID, Text: delta,
		})
	})
	return streamedTurnRound{Content: content.String(), Reasoning: reasoning.String(), Response: response}, err
}

// buildPromptCachePolicy computes the provider-neutral prompt-cache intent for
// a turn. The cache key is derived from [providerId, model, conversationId] so
// the provider can dedup cache entries across requests in the same
// conversation. When prompt caching is disabled in settings, returns nil.
// Mirrors the TS promptCacheKey + AgentPromptCachePolicy.
func buildPromptCachePolicy(settings domain.Settings, providerID, model, conversationID string) *PromptCachePolicy {
	if !settings.PromptCaching {
		return nil
	}
	canonical, _ := json.Marshal([3]string{providerID, model, conversationID})
	sum := sha256.Sum256(canonical)
	return &PromptCachePolicy{
		Mode: "auto",
		Key:  "pc_" + hex.EncodeToString(sum[:]),
	}
}

// buildHydration assembles the synthetic runtime-hydration transcript from the
// App's read-only stores. The transcript is ephemeral — never persisted in the
// conversation — and is rebuilt fresh for each provider request so runtime
// facts (date, workspace, memory, skills, MCP catalog, tool catalog) stay
// current.
func (a *App) buildHydration(c *domain.Conversation) []ChatMessage {
	source := HydrationSource{
		RuntimeContext: DefaultRuntimeContext(c.Workspace),
	}
	if a.Memory != nil {
		source.Memory = a.Memory
	}
	if a.Skills != nil {
		source.Skills = a.Skills
	}
	if a.MCP != nil {
		source.MCPServers = a.MCP
	}
	if a.MCPToolbox != nil {
		source.MCP = a.MCPToolbox
	}
	if a.Toolbox != nil {
		source.Tools = a.Toolbox.ListTools()
	}
	if a.Todos != nil {
		source.Todos = a.Todos
		source.ConvID = c.ID
	}
	return NewHydrationBuilder(source).Build().Messages
}

func (a *App) completeWithRetry(ctx context.Context, adapter AIProvider, request ChatRequest) (ChatResponse, error) {
	for retry := 1; ; retry++ {
		response, err := adapter.Complete(ctx, request)
		if err == nil || retry >= maxProviderAttempts {
			return response, err
		}
		delay, retryable := providerRetryDelay(err, retry)
		if !retryable {
			return response, err
		}
		a.log("warn", "ai", "retrying provider completion (%d/%d) after %s: %v", retry, maxProviderAttempts, delay.Round(time.Millisecond), err)
		if err := a.retrySleeper(ctx, delay); err != nil {
			return ChatResponse{}, err
		}
	}
}

func (a *App) persistTurnRound(conversationID, messageID, model string, round streamedTurnRound) error {
	conversation, err := a.Conversations.Get(conversationID)
	if err != nil {
		return err
	}

	a.updateMessage(conversation, messageID, func(message *domain.Message) {
		applyStreamRound(message, model, round)
		message.Status = domain.StatusDone
	})

	newToolCalls := make([]domain.ToolCall, 0, len(round.Response.ToolCalls))
	for _, toolCall := range round.Response.ToolCalls {
		if !a.hasToolCall(conversation, messageID, toolCall.ID) {
			conversation = a.appendToolCall(conversation, messageID, toolCall)
			newToolCalls = append(newToolCalls, toolCall)
		}
	}
	if len(newToolCalls) > 0 {
		a.updateMessage(conversation, messageID, func(message *domain.Message) {
			message.Steps = append(message.Steps, domain.MessageStep{Type: domain.StepToolCalls, ToolCalls: newToolCalls})
		})
	}
	return a.Conversations.Save(conversation)
}

func (a *App) persistPartialTurnRound(conversationID, messageID, model string, round streamedTurnRound) error {
	// A partial stream must never carry an unconfirmed tool call into the next
	// continuation request. Tools run only after a fully completed round.
	round.Response.ToolCalls = nil
	return a.persistTurnRound(conversationID, messageID, model, round)
}

func applyStreamRound(message *domain.Message, model string, round streamedTurnRound) {
	if round.Reasoning != "" {
		message.Steps = append(message.Steps, domain.MessageStep{Type: domain.StepReasoning, Content: round.Reasoning})
		message.Reasoning = round.Reasoning
	}
	if round.Content != "" {
		message.Steps = append(message.Steps, domain.MessageStep{Type: domain.StepText, Content: round.Content})
		message.Content = round.Content
	}
	message.Model = model
	message.Usage = toDomainUsage(round.Response.Usage)
}

func (a *App) executeTurnTools(run *TurnRun, messageID string, toolCalls []domain.ToolCall) error {
	for i, toolCall := range toolCalls {
		if err := run.Ctx.Err(); err != nil {
			a.interruptRemainingTools(run, messageID, toolCalls[i:])
			return err
		}
		a.Bus.Emit(contracts.EventToolStarted, contracts.ToolStartedEvent{
			RunID: run.ID, ConversationID: run.ConversationID, ToolCallID: toolCall.ID, Name: toolCall.Name, Args: []byte(toolCall.Args),
		})
		a.log("info", "tools", "tool call: %s", toolCall.Name)
		output, err := a.Toolbox.Execute(WithConversationID(run.Ctx, run.ConversationID), toolCall.Name, []byte(toolCall.Args))
		status := domain.ToolOK
		if err != nil {
			if run.Ctx.Err() != nil {
				status = domain.ToolInterrupted
				output = "interrupted"
			} else {
				status = domain.ToolFailed
				output = "error: " + err.Error()
			}
		}
		a.Bus.Emit(contracts.EventToolCompleted, contracts.ToolCompletedEvent{
			RunID: run.ID, ConversationID: run.ConversationID, ToolCallID: toolCall.ID,
			Name: toolCall.Name, Status: string(status), Output: output,
		})
		// When the model updates the todo checklist, emit a dedicated event so
		// the UI can re-render the strip without polling agent.todos.get.
		if toolCall.Name == "todo" && status == domain.ToolOK && a.Todos != nil {
			items := a.Todos.Get(run.ConversationID)
			dtos := make([]contracts.TodoItemDTO, 0, len(items))
			for _, item := range items {
				dtos = append(dtos, contracts.TodoItemDTO{ID: item.ID, Content: item.Content, Status: string(item.Status)})
			}
			summary := domain.SummarizeTodos(items)
			a.Bus.Emit(contracts.EventTodoUpdated, contracts.TodoUpdatedEvent{
				ConversationID: run.ConversationID,
				Items:          dtos,
				Summary:        contracts.TodoSummaryDTO{Total: summary.Total, Pending: summary.Pending, InProgress: summary.InProgress, Completed: summary.Completed},
			})
		}

		conversation, convErr := a.Conversations.Get(run.ConversationID)
		if convErr != nil {
			return convErr
		}
		conversation = a.updateToolResult(conversation, messageID, toolCall.ID, status, output)
		if saveErr := a.Conversations.Save(conversation); saveErr != nil {
			return saveErr
		}
		if err := run.Ctx.Err(); err != nil {
			a.interruptRemainingTools(run, messageID, toolCalls[i+1:])
			return err
		}
	}
	return nil
}

func (a *App) interruptRemainingTools(run *TurnRun, messageID string, toolCalls []domain.ToolCall) {
	if len(toolCalls) == 0 {
		return
	}
	conversation, err := a.Conversations.Get(run.ConversationID)
	if err != nil {
		return
	}
	for _, toolCall := range toolCalls {
		a.Bus.Emit(contracts.EventToolCompleted, contracts.ToolCompletedEvent{
			RunID: run.ID, ConversationID: run.ConversationID, ToolCallID: toolCall.ID,
			Name: toolCall.Name, Status: string(domain.ToolInterrupted), Output: "interrupted",
		})
		conversation = a.updateToolResult(conversation, messageID, toolCall.ID, domain.ToolInterrupted, "interrupted")
	}
	_ = a.Conversations.Save(conversation)
}

func (a *App) appendTurnAssistant(conversationID string) (*domain.Conversation, string, error) {
	conversation, err := a.Conversations.Get(conversationID)
	if err != nil {
		return nil, "", err
	}
	next := domain.Message{ID: domain.NewID("msg"), Role: domain.RoleAssistant, CreatedAt: time.Now().UTC()}
	conversation.AddMessage(next)
	if err := a.Conversations.Save(conversation); err != nil {
		return nil, "", err
	}
	return conversation, next.ID, nil
}

func (a *App) finishTurn(run *TurnRun, messageID, model string, usage ChatUsage) error {
	conversation, err := a.Conversations.Get(run.ConversationID)
	if err != nil {
		return err
	}
	conversation.Status = "idle"
	conversation.Touch()
	if err := a.Conversations.Save(conversation); err != nil {
		return err
	}
	a.Bus.Emit(contracts.EventTurnDone, contracts.TurnDoneEvent{
		RunID: run.ID, ConversationID: run.ConversationID, MessageID: messageID, Model: model,
		Usage: &contracts.UsageDTO{
			InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
			CacheRead: usage.CacheRead, CacheWrite: usage.CacheWrite,
		},
	})
	a.log("info", "agent", "turn finished: %s (in %d / out %d)", run.ID, usage.InputTokens, usage.OutputTokens)

	// Threshold-based learning review: accumulate turns since the last
	// review and only fire when the threshold is reached. This avoids
	// burning extraction cycles on short "hi/thanks" exchanges. The
	// compaction hook (subscribeCompactionReview) fires independently
	// when a conversation is compacted, so learning still happens for
	// long conversations that compact before reaching the threshold.
	a.incrementTurnCounter(conversation.ID)

	return nil
}

// incrementTurnCounter bumps the per-conversation turn counter and
// triggers a learning review if the threshold is reached. The threshold
// is read from settings (default 50, 0 disables turn-based review).
func (a *App) incrementTurnCounter(conversationID string) {
	if a.LearningReviewer == nil {
		return
	}
	threshold := a.Settings.Get().LearningReviewThreshold
	if threshold <= 0 {
		return // turn-based review disabled
	}
	a.learningMu.Lock()
	a.turnsSinceReview[conversationID]++
	count := a.turnsSinceReview[conversationID]
	a.learningMu.Unlock()
	if count < threshold {
		return
	}
	a.flushLearningReview(conversationID)
}

// flushLearningReview resets the turn counter for a conversation and
// fires a background review over all accumulated messages since the
// last review. Called when the threshold is reached or when a
// compaction event fires (whichever comes first).
func (a *App) flushLearningReview(conversationID string) {
	a.learningMu.Lock()
	a.turnsSinceReview[conversationID] = 0
	a.learningMu.Unlock()

	conversation, err := a.Conversations.Get(conversationID)
	if err != nil {
		return
	}
	// Extract from the full conversation — the reviewer's dedup logic
	// handles repeats. This catches signals that were spread across
	// multiple turns, not just the last one.
	var userMsgs, assistantMsgs []string
	for _, m := range conversation.Messages {
		switch m.Role {
		case domain.RoleUser:
			userMsgs = append(userMsgs, m.Content)
		case domain.RoleAssistant:
			assistantMsgs = append(assistantMsgs, m.Content)
		}
	}
	combined := joinMessages(userMsgs) + "\n---\n" + joinMessages(assistantMsgs)
	if a.Trajectory != nil {
		a.Trajectory.Record("review", map[string]interface{}{
			"conversation": conversationID,
			"user_msgs":    len(userMsgs),
			"asst_msgs":    len(assistantMsgs),
		})
	}
	a.ReviewTurnAsync(context.Background(), conversationID, combined, combined)
}

// joinMessages concatenates messages with newlines for batch extraction.
func joinMessages(msgs []string) string {
	if len(msgs) == 0 {
		return ""
	}
	out := ""
	for i, m := range msgs {
		if i > 0 {
			out += "\n"
		}
		out += m
	}
	return out
}
