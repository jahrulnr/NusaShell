package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

// defaultMaxParallelTools is the fallback concurrency bound for tool calls
// from a single assistant round when settings.MaxParallelTools is not set.
// The actual bound is settings.MaxParallelTools (default 6, range 1–64,
// configurable in Settings).
const defaultMaxParallelTools = 6

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
	contextWindow := a.resolveContextWindow(provider, model, settings)
	maxOutput := resolveMaxOutput(provider, model, settings)
	compactionTrigger := compactionTriggerTokens(contextWindow, maxOutput, settings)
	beforeTokens := conversation.EstimateTokens()
	if !settings.CompactionEnabled || beforeTokens <= compactionTrigger {
		return adapter, conversation, settings, nil
	}

	a.log("info", "agent", "compaction triggered for %s: est=%d trigger=%d window=%d maxOut=%d",
		conversation.ID, beforeTokens, compactionTrigger, contextWindow, maxOutput)
	compAdapter, compModel, compWindow := a.resolveCompactionAdapter(run.Ctx, adapter, model, contextWindow, settings)
	summary, err := a.compactConversation(run.Ctx, compAdapter, conversation, compModel, compWindow, settings)
	if err != nil {
		a.log("warn", "agent", "compaction failed for %s: %v", conversation.ID, err)
		a.Bus.Emit(contracts.EventCompactionFailed, contracts.CompactionFailedEvent{ConversationID: conversation.ID, Error: err.Error()})
	} else {
		a.Bus.Emit(contracts.EventCompacted, contracts.CompactedEvent{ConversationID: conversation.ID, Summary: summary})
		a.log("info", "agent", "compacted conversation %s", conversation.ID)
	}
	conversation, err = a.Conversations.Get(run.ConversationID)
	afterTokens := conversation.EstimateTokens()
	a.log("info", "agent", "compaction result for %s: before=%d after=%d (msgs=%d)",
		conversation.ID, beforeTokens, afterTokens, len(conversation.Messages))
	return adapter, conversation, settings, err
}

func (a *App) toolDefinitions() []ToolDef {
	// Roster for providers: non-family built-ins plus the dispatcher family
	// definitions. Root+op is the single naming layer (see
	// docs/design/tool-dispatchers.md).
	tools := append(a.Toolbox.ListTools(), DispatcherToolInfos()...)
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

func (a *App) streamTurnRound(run *TurnRun, adapter AIProvider, conversation *domain.Conversation, messageID, model, effort string, tools []ToolDef, settings domain.Settings, continuation bool, maxTokens int, injectHydration bool, promptCache *PromptCachePolicy, caps ModelCapabilities) (streamedTurnRound, error) {
	for retry := 1; ; retry++ {
		roundResult, err := a.streamTurnRoundOnce(run, adapter, conversation, messageID, model, effort, tools, settings, continuation, maxTokens, injectHydration, promptCache, caps)
		if err == nil || retry >= maxProviderAttempts || roundResult.Content != "" || roundResult.Reasoning != "" {
			return roundResult, err
		}
		// Dynamic 400-learning: classify the error body and, when it
		// matches a known pattern (unsupported param / required field /
		// text-only model), record it to the persisted registry. If the
		// learned action upgrades ReasoningReplay (inject
		// reasoning_content) or disables a modality (text-only model
		// rejecting images), refresh caps so the next retry adapts.
		// This catches models not in the catalog (e.g. stealth/ox-alpha
		// on OpenRouter that 400s with "reasoning_content must be passed
		// back", or Qwen3.8 that 400s with "text-only; must be a text
		// part").
		if isLearnable400(err) {
			body := extractErrBody(err)
			action, param := a.learnedParams.LearnFrom400(run.ProviderID, model, body)
			providerName := a.providerNameByID(run.ProviderID)
			if action == domain.LearnedActionInject && strings.EqualFold(param, stripReasoningContentParam) {
				if !caps.ReasoningReplay {
					caps.ReasoningReplay = true
					a.log("info", "learning", "upgraded ReasoningReplay for %s/%s from 400 learning", providerName, model)
				}
			}
			if action == domain.LearnedActionDisableModality {
				if strings.EqualFold(param, "vision") && caps.Vision {
					caps.Vision = false
					a.log("info", "learning", "disabled Vision for %s/%s from 400 learning (text-only model)", providerName, model)
				}
				if strings.EqualFold(param, "audio") && caps.Audio {
					caps.Audio = false
					a.log("info", "learning", "disabled Audio for %s/%s from 400 learning", providerName, model)
				}
				if strings.EqualFold(param, "video") && caps.Video {
					caps.Video = false
					a.log("info", "learning", "disabled Video for %s/%s from 400 learning", providerName, model)
				}
			}
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

func (a *App) streamTurnRoundOnce(run *TurnRun, adapter AIProvider, conversation *domain.Conversation, messageID, model, effort string, tools []ToolDef, settings domain.Settings, continuation bool, maxTokens int, injectHydration bool, promptCache *PromptCachePolicy, caps ModelCapabilities) (streamedTurnRound, error) {
	var content strings.Builder
	var reasoning strings.Builder
	// The system prompt is deliberately cache-stable: identity, user
	// instructions, system-level skill messages, and workspace only. No
	// runtime state (delegation config, continuation instructions, async
	// results) is appended here — those travel as tool hydration or tool
	// descriptions so the system prefix keeps its prompt-cache hits.
	system := buildSystemPrompt(conversation, settings.UserPrompt)
	// Persist the synthetic runtime-hydration transcript (runtime_context,
	// memory, skill, mcp_list, tool_list, todo_list) to the conversation
	// store as an assistant message with hydration tool calls + matching tool
	// results. Persisting (rather than injecting ephemerally) keeps the
	// message-list prefix stable across tool rounds, so the provider can
	// reuse prompt-cache hits from round 1 on round 2+.
	//
	// Hydration is persisted once per history epoch. Normal user messages and
	// steers reuse the checkpoint already present in conversation history.
	// Compaction strips that checkpoint, so the first post-compaction round
	// creates a fresh one. Re-injecting it while history is intact causes smaller
	// models to misinterpret the synthetic tool calls as a pattern to repeat
	// ("call all tools in parallel every round") and wastes context.
	//
	// The hydration messages are marked with the "hydrate-" tool call ID
	// prefix so the UI can filter them out of the visible conversation and
	// compaction can strip them before summarization.
	if injectHydration && !HasHydration(chatMessages(conversation, messageID, caps)) {
		hydrationMsgs := a.buildHydration(conversation)
		conversation = a.persistHydration(conversation, hydrationMsgs)
		// Mark the assistant message for this turn so the UI can show a
		// "context updated" badge — the hydration checkpoint was freshly
		// persisted, meaning runtime facts (date, memory, skills, MCP, tools)
		// were refreshed for this turn.
		a.updateMessage(conversation, messageID, func(message *domain.Message) {
			message.ContextUpdated = true
		})
		_ = a.Conversations.Save(conversation)
	}
	messages := a.chatMessagesForProvider(conversation, messageID, caps)
	// Continuation rounds (partial-stream recovery, failed-message retry)
	// inject the "continue from where you stopped" instruction as an
	// ephemeral synthetic tool call + result instead of a system prompt
	// mutation. The model processes it like any tool output and continues
	// the interrupted response; nothing is persisted, so later rounds keep
	// the cache-stable system prefix.
	if continuation {
		messages = appendContinuationTool(messages)
	}
	// Publish a lightweight server-side context estimate (system + messages +
	// tool definitions as actually sent) so the UI badge is not just a guess
	// from the transcript alone — and remember it on the conversation so the
	// idle badge shows the same number.
	if a.Bus != nil {
		est := estimateRequestTokens(system, messages, tools)
		a.Bus.Emit(contracts.EventContextEstimate, contracts.ContextEstimateEvent{
			RunID: run.ID, ConversationID: run.ConversationID, MessageID: messageID,
			EstimatedTokens: est,
		})
		if conversation.EstimatedTokens != est {
			conversation.EstimatedTokens = est
			_ = a.Conversations.Save(conversation)
		}
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
		ReasoningReplay:  caps.ReasoningReplay,
		StripParams:      a.learnedParams.StripParams(run.ProviderID, model),
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

// appendContinuationTool appends the synthetic continue_stream tool call
// (with its result pre-filled, announcement-style) to the provider message
// list for a continuation round. Ephemeral: it exists only in this
// request, never persisted to the conversation store.
func appendContinuationTool(messages []ChatMessage) []ChatMessage {
	id := domain.ContinueStreamToolCallPrefix + randomNonce()
	call := domain.ToolCall{ID: id, Name: domain.ContinueStreamToolName, Args: "{}", Status: domain.ToolOK, Output: domain.ContinueStreamMessage}
	return append(messages,
		ChatMessage{Role: "assistant", ToolCalls: []domain.ToolCall{call}},
		ChatMessage{Role: "tool", ToolResult: &ToolResult{ToolCallID: id, Name: domain.ContinueStreamToolName, Content: domain.ContinueStreamMessage}},
	)
}

// estimateRequestTokens approximates provider tokens from the real request
// payload: system + messages + tools JSON. ~4 chars/token with a surcharge
// for non-ASCII (CJK-ish) characters, ~4 tokens per-message overhead, ~150
// tokens per image attachment, plus a 5% safety buffer.
func estimateRequestTokens(system string, messages []ChatMessage, tools []ToolDef) int64 {
	chars := int64(len(system))
	totalOverhead := int64(4 * len(messages))
	images := 0
	raw, _ := json.Marshal(messages)
	chars += int64(len(raw))
	if len(tools) > 0 {
		rawTools, _ := json.Marshal(tools)
		chars += int64(len(rawTools))
	}
	// Non-ASCII cost more (CJK ≈ 1–2 tokens each): add ~1 token per non-ASCII
	// rune so unicode-heavy threads are not undercounted.
	for _, m := range messages {
		if len(m.Attachments) > 0 {
			for _, a := range m.Attachments {
				if a.Type == "image" {
					images++
				}
			}
		}
	}
	chars += int64(images * 150)
	tokens := (chars + totalOverhead) / 4
	return int64(float64(tokens) * 1.05)
}

func buildPromptCachePolicy(settings domain.Settings, providerID, model, conversationID string) *PromptCachePolicy {
	if !settings.PromptCaching {
		return nil
	}
	canonical, _ := json.Marshal([3]string{providerID, model, conversationID})
	sum := sha256.Sum256(canonical)
	// OpenAI caps prompt_cache_key at 64 chars; use 32 total (pc_ + 29 hex
	// chars from the sha256 digest) for a comfortable margin below the
	// limit while keeping plenty of cardinality for cache routing.
	full := hex.EncodeToString(sum[:])
	return &PromptCachePolicy{
		Mode: "auto",
		Key:  "pc_" + full[:29],
	}
}

// persistHydration appends the synthetic hydration messages (assistant
// toolCalls + matching tool results) to the conversation store and saves.
// The messages are marked with the "hydrate-" tool call ID prefix so the UI
// can filter them out and compaction can strip them before summarization.
// Returns the updated conversation.
func (a *App) persistHydration(c *domain.Conversation, msgs []ChatMessage) *domain.Conversation {
	if len(msgs) == 0 {
		return c
	}
	// The first message is the assistant message with hydration tool calls.
	// Subsequent messages are the tool results.
	for _, m := range msgs {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			hydMsg := domain.Message{
				ID:        domain.NewID("msg"),
				Role:      domain.RoleAssistant,
				ToolCalls: m.ToolCalls,
				Status:    domain.StatusDone,
				CreatedAt: time.Now().UTC(),
			}
			c.Messages = append(c.Messages, hydMsg)
		} else if m.Role == "tool" && m.ToolResult != nil {
			// Attach tool results to the last assistant message's tool calls.
			for i := len(c.Messages) - 1; i >= 0; i-- {
				if c.Messages[i].Role == domain.RoleAssistant && len(c.Messages[i].ToolCalls) > 0 {
					for j := range c.Messages[i].ToolCalls {
						if c.Messages[i].ToolCalls[j].ID == m.ToolResult.ToolCallID {
							c.Messages[i].ToolCalls[j].Output = m.ToolResult.Content
							break
						}
					}
					break
				}
			}
		}
	}
	_ = a.Conversations.Save(c)
	return c
}

// buildHydration assembles a synthetic runtime-hydration checkpoint from the
// App's read-only stores when the current history epoch does not already have
// one, normally on the initial turn or immediately after compaction.
func (a *App) buildHydration(c *domain.Conversation) []ChatMessage {
	ctx := DefaultRuntimeContext(c.Workspace)
	ctx.DataDir = a.DataDir
	source := HydrationSource{
		RuntimeContext: ctx,
	}
	if a.Toolbox != nil {
		// The real toolbox executes the meta-tools (mcp_list, tool_list per
		// server, skill, memory op=list) so the checkpoint contains genuine
		// tool output — the same tools the agent calls.
		source.Executor = a.Toolbox
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

// toolExecResult is one tool's outcome from the concurrent execution phase,
// held until results are persisted in tool-call order.
type toolExecResult struct {
	status domain.ToolCallStatus
	output string
	atts   []domain.Attachment
}

func (a *App) executeTurnTools(run *TurnRun, messageID string, toolCalls []domain.ToolCall, caps ModelCapabilities, settings domain.Settings) error {
	if err := run.Ctx.Err(); err != nil {
		a.interruptRemainingTools(run, messageID, toolCalls)
		return err
	}

	// Phase 1: execute all tool calls concurrently (bounded). Only tool
	// execution and event emission happen here — no conversation-store writes —
	// so each backing store's own lock is sufficient and there are no
	// read-modify-write races on the conversation snapshot. Results are kept in
	// tool-call order for deterministic persistence in phase 2.
	results := make([]toolExecResult, len(toolCalls))
	var wg sync.WaitGroup
	limit := settings.MaxParallelTools
	if limit < 1 {
		limit = defaultMaxParallelTools
	}
	sem := make(chan struct{}, limit)
	for i := range toolCalls {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = a.runOneTool(run, toolCalls[i], caps, settings)
		}(i)
	}
	wg.Wait()

	// Phase 2: persist results in tool-call order and emit todo updates. This
	// runs on the single turn goroutine, so conversation snapshot writes never
	// race with each other.
	conversation, err := a.Conversations.Get(run.ConversationID)
	if err != nil {
		return err
	}
	for i := range toolCalls {
		toolCall := toolCalls[i]
		r := results[i]
		conversation = a.updateToolResult(conversation, messageID, toolCall.ID, r.status, r.output, r.atts)
		// When the model updates the todo checklist, emit a dedicated event so
		// the UI can re-render the strip without polling agent.todos.get.
		if toolCall.Name == "todo" && r.status == domain.ToolOK && a.Todos != nil {
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
				Brief:          a.Todos.GetBrief(run.ConversationID),
			})
		}
	}
	if saveErr := a.Conversations.Save(conversation); saveErr != nil {
		return saveErr
	}
	if err := run.Ctx.Err(); err != nil {
		return err
	}
	return nil
}

// runOneTool executes a single tool call and returns its result. It emits the
// tool-started and tool-completed events and never writes to the conversation
// store, so it is safe to run concurrently for the tool calls of one round.
func (a *App) runOneTool(run *TurnRun, toolCall domain.ToolCall, caps ModelCapabilities, settings domain.Settings) toolExecResult {
	a.Bus.Emit(contracts.EventToolStarted, contracts.ToolStartedEvent{
		RunID: run.ID, ConversationID: run.ConversationID, ToolCallID: toolCall.ID, Name: toolCall.Name, Args: []byte(toolCall.Args),
	})
	a.log("info", "tools", "tool call: %s", toolCall.Name)

	// If the turn was already cancelled, do not start the tool — mark it
	// interrupted (mirrors the pre-parallel behavior of skipping remaining
	// tools after cancellation).
	if run.Ctx.Err() != nil {
		res := toolExecResult{status: domain.ToolInterrupted, output: "interrupted by user"}
		a.emitToolCompleted(run, toolCall, res)
		return res
	}

	var output string
	var outputAttachments []domain.Attachment
	var err error
	switch toolCall.Name {
	case "read_media":
		kind, sniffErr := sniffMediaKind([]byte(toolCall.Args))
		if sniffErr != nil {
			output = "error: " + sniffErr.Error()
			err = sniffErr
		} else {
			switch kind {
			case "image":
				output, outputAttachments, err = a.executeReadImage(run, toolCall, caps, settings)
			case "audio":
				output, outputAttachments, err = a.executeReadAudio(run, toolCall, caps, settings)
			case "video":
				output, outputAttachments, err = a.executeReadVideo(run, toolCall, caps, settings)
			case "document":
				output, outputAttachments, err = a.executeReadDocument(run, toolCall, caps, settings)
			default:
				output = "error: unrecognized media type"
				err = fmt.Errorf("unrecognized media type")
			}
		}
	case "generate_media", "generate_image", "generate_speech", "generate_video":
		output, outputAttachments, err = a.executeGenerateMedia(run, toolCall, settings)
	default:
		toolCtx := WithConversationID(run.Ctx, run.ConversationID)
		toolCtx = WithRunID(toolCtx, run.ID)
		toolCtx = WithToolCallID(toolCtx, toolCall.ID)
		output, err = a.Toolbox.Execute(toolCtx, toolCall.Name, []byte(toolCall.Args))
	}
	status := domain.ToolOK
	if err != nil {
		if run.Ctx.Err() != nil {
			status = domain.ToolInterrupted
			output = "interrupted by user"
		} else {
			status = domain.ToolFailed
			output = "error: " + truncateToolError(err.Error())
		}
	}
	// Async subagent: the tool returns immediately with "starting" status.
	// Keep the tool call marked as running so the UI shows a spinner; the
	// OnDone callback will update it to ok/fail with the summary when the
	// subagent finishes.
	if toolCall.Name == "subagent" && err == nil {
		status = domain.ToolRunning
	}
	res := toolExecResult{status: status, output: output, atts: outputAttachments}
	a.emitToolCompleted(run, toolCall, res)
	a.emitLearningMutationEvents(toolCall.Name, status)
	// Skill nudge: count tool calls per conversation so tool-heavy but
	// user-turn-light coding sessions trigger skill review independently
	// of the turn threshold.
	if status == domain.ToolOK || status == domain.ToolFailed {
		a.incrementToolCallCounter(run.ConversationID)
	}
	return res
}

func (a *App) emitToolCompleted(run *TurnRun, toolCall domain.ToolCall, res toolExecResult) {
	event := contracts.ToolCompletedEvent{
		RunID: run.ID, ConversationID: run.ConversationID, ToolCallID: toolCall.ID,
		Name: toolCall.Name, Status: string(res.status), Output: res.output,
	}
	for _, att := range res.atts {
		event.Attachments = append(event.Attachments, contracts.AttachmentDTO{
			Type: att.Type, Name: att.Name, MediaType: att.MediaType, FilePath: att.FilePath,
		})
	}
	a.Bus.Emit(contracts.EventToolCompleted, event)
}

// emitLearningMutationEvents publishes memory.updated and/or skill.updated
// events when a tool mutates the memory or skill stores, so the Learning UI
// can refresh its memory list, search results, and graph in real time without
// polling. Only successful tool calls trigger the events.
func (a *App) emitLearningMutationEvents(toolName string, status domain.ToolCallStatus) {
	if a.Bus == nil || status != domain.ToolOK {
		return
	}
	// Route by dispatcher root; any successful family op refreshes the
	// Learning UI panes in real time.
	switch toolName {
	case "memory":
		a.Bus.Emit(contracts.EventMemoryUpdated, map[string]any{
			"source": "tool",
			"tool":   toolName,
		})
	case "skill":
		a.Bus.Emit(contracts.EventSkillUpdated, map[string]any{
			"source": "tool",
			"tool":   toolName,
		})
	}
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
			Name: toolCall.Name, Status: string(domain.ToolInterrupted), Output: "interrupted by user",
		})
		conversation = a.updateToolResult(conversation, messageID, toolCall.ID, domain.ToolInterrupted, "interrupted by user", nil)
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

func (a *App) finishTurn(run *TurnRun, messageID, model string, usage ChatUsage, contextTokens, autoContinueIndex int) error {
	conversation, err := a.Conversations.Get(run.ConversationID)
	if err != nil {
		return err
	}
	conversation.Status = "idle"
	// Record the authoritative provider-measured context fill (last round's
	// input + cached input + output) as the source of truth for the idle
	// badge. Providers that report no usage leave it at zero, and the UI
	// falls back to the heuristic EstimatedTokens.
	if contextTokens > 0 {
		conversation.ContextTokens = int64(contextTokens)
	}
	conversation.Touch()
	if err := a.Conversations.Save(conversation); err != nil {
		return err
	}

	// Compute the outer multi-turn auto-continue decision. Only attached
	// when a todo port is configured; failed/cancelled paths omit it.
	var autoContinue *contracts.AutoContinueDTO
	if a.Todos != nil {
		items := a.Todos.Get(run.ConversationID)
		lastText := lastAssistantText(conversation, messageID)
		decision := domain.DecideAutoContinue(domain.AutoContinueInput{
			Items:             items,
			AutoContinueIndex: autoContinueIndex,
			MaxAutoContinues:  a.Settings.Get().MaxAutoContinues,
			TurnOK:            true,
			HasConversation:   true,
			TurnText:          lastText,
			HasBackgroundJobs: a.hasPendingSubagents(run.ConversationID),
		})
		autoContinue = &contracts.AutoContinueDTO{
			ShouldContinue:   decision.ShouldContinue,
			OpenTodoCount:    decision.OpenTodoCount,
			ContinuesUsed:    decision.ContinuesUsed,
			MaxAutoContinues: decision.MaxAutoContinues,
			Reason:           string(decision.Reason),
		}
	}

	a.Bus.Emit(contracts.EventTurnDone, contracts.TurnDoneEvent{
		RunID: run.ID, ConversationID: run.ConversationID, MessageID: messageID, Model: model,
		Usage: &contracts.UsageDTO{
			InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
			CacheRead: usage.CacheRead, CacheWrite: usage.CacheWrite,
		},
		ContextTokens: contextTokens,
		AutoContinue:  autoContinue,
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

// lastAssistantText returns the visible text content of the assistant message
// with the given ID. Used by the auto-continue policy to detect whether the
// turn ended with a question (which means the agent is waiting for the user).
func lastAssistantText(c *domain.Conversation, messageID string) string {
	return domain.LastAssistantText(c, messageID)
}

// incrementTurnCounter bumps the per-conversation turn counter and
// triggers a learning review if the threshold is reached. The threshold
// is read from settings (default 10, 0 disables turn-based review).
// Counters are persisted to disk so they survive server restarts.
func (a *App) incrementTurnCounter(conversationID string) {
	if a.ReviewAgent == nil {
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
	// Persist so the counter survives restarts.
	a.saveTurnCounters()
	if count < threshold {
		return
	}
	a.flushLearningReview(conversationID, "threshold")
}

// incrementToolCallCounter bumps the per-conversation tool-call counter and
// triggers a learning review if the skill-nudge threshold is reached. This
// catches skill-worthy patterns in tool-heavy but user-turn-light coding
// sessions that would never reach the turn threshold. The threshold is read
// from settings (default 15, 0 disables tool-based review). Counters are
// persisted to disk so they survive server restarts.
func (a *App) incrementToolCallCounter(conversationID string) {
	if a.ReviewAgent == nil {
		return
	}
	threshold := a.Settings.Get().SkillNudgeInterval
	if threshold <= 0 {
		return // tool-based review disabled
	}
	a.learningMu.Lock()
	a.toolCallsSinceReview[conversationID]++
	count := a.toolCallsSinceReview[conversationID]
	a.learningMu.Unlock()
	a.saveTurnCounters()
	if count < threshold {
		return
	}
	a.flushLearningReview(conversationID, "skill_nudge")
}

// flushLearningReview resets the turn counter for a conversation and
// fires a background LLM review over the recent transcript. Called when
// the threshold is reached or when a compaction event fires (whichever
// comes first). The review uses the conversation's configured model
// (the "global LLM") with a restricted toolset and the review
// prompt. It is fire-and-forget — it never blocks or fails the parent
// turn.
func (a *App) flushLearningReview(conversationID string, reason string) {
	a.log("info", "learning", "review triggered: conv=%s reason=%s", conversationID, reason)
	if a.ReviewAgent == nil {
		return
	}
	// Reserve synchronously before launching the worker. This makes the
	// counter transition deterministic: rejected/coalesced triggers retain
	// their evidence, while a trigger that wins the slot resets counters
	// immediately. The LLM work remains fire-and-forget below.
	if !a.ReviewAgent.reserveReview(conversationID) {
		return
	}
	a.learningMu.Lock()
	a.turnsSinceReview[conversationID] = 0
	a.toolCallsSinceReview[conversationID] = 0
	a.learningMu.Unlock()
	a.saveTurnCounters()
	a.goSafe("learning", func() {
		if a.Bus != nil {
			a.Bus.Emit(contracts.EventLearningReviewStarted, contracts.LearningReviewEvent{
				ConversationID: conversationID,
				Status:         "started",
				Reason:         reason,
			})
		}
		err := a.ReviewAgent.runReservedReview(context.Background(), conversationID)
		if err != nil {
			a.ReviewAgent.recordReviewError(conversationID, err.Error())
			if a.Bus != nil {
				a.Bus.Emit(contracts.EventLearningReviewError, contracts.LearningReviewEvent{
					ConversationID: conversationID,
					Status:         "error",
					Error:          err.Error(),
				})
			}
			return
		}
		if a.Bus != nil {
			a.Bus.Emit(contracts.EventLearningReviewDone, contracts.LearningReviewEvent{
				ConversationID: conversationID,
				Status:         "done",
				Reason:         reason,
			})
		}
	})
}

// truncateToolError prevents oversized error messages from wasting tokens
// when a provider returns an error that embeds base64 media data (e.g.
// "Failed to load image from data:audio/mpeg;base64,//NkxAAAA..."). Such
// errors can be 1.5MB+ and pollute the conversation history, causing every
// subsequent request to fail with HTTP 400. The error is truncated to a
// reasonable length while preserving the diagnostic prefix.
const maxToolErrorLen = 2000

func truncateToolError(msg string) string {
	if len(msg) <= maxToolErrorLen {
		return msg
	}
	return msg[:maxToolErrorLen] + "...[truncated: original error was " +
		strconv.Itoa(len(msg)) + " chars]"
}
