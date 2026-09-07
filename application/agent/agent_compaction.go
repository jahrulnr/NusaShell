package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"nusashell/application/service/tooloutput"
	"nusashell/domain"
	"nusashell/pkg/text"
	"nusashell/resources"
)

func (a *Service) ResolveCompactionAdapter(ctx context.Context, defaultAdapter ProviderContext, defaultModel string, defaultWindow int, settings domain.Settings) (ProviderContext, string, int) {
	compModel := strings.TrimSpace(settings.CompactionModel)
	if compModel == "" {
		return defaultAdapter, defaultModel, defaultWindow
	}
	// Server-side compaction (context_management) is handled by the server
	// during the normal stream call, not by a separate compaction model.
	// Skip the compaction-model override when the chat model is server-side
	// eligible so the compaction item stays valid for the same model.
	if domain.CodexSupportsRemoteCompaction(defaultAdapter.Kind) || domain.OpenAISupportsServerCompaction(defaultModel) {
		return defaultAdapter, defaultModel, defaultWindow
	}
	provider, bareModel, apiKey, rpcErr := a.resolveModel(compModel)
	if rpcErr != nil || provider == nil {
		a.log("warn", "agent", "compaction model %q could not be resolved, falling back to chat model: %v", compModel, rpcErr)
		return defaultAdapter, defaultModel, defaultWindow
	}
	adapter, err := a.Factory(ctx, provider, apiKey)
	if err != nil {
		a.log("warn", "agent", "compaction model %q adapter build failed, falling back to chat model: %v", compModel, err)
		return defaultAdapter, defaultModel, defaultWindow
	}
	pc := NewProviderContext(provider, adapter)
	window := a.ResolveContextWindow(provider, bareModel, settings)
	a.log("info", "agent", "compaction using override model %s (window=%d)", compModel, window)
	return pc, bareModel, window
}

// resolveContextWindow picks the effective context window for compaction
// decisions: min(model context, max_input_tokens) when both are known, or the
// configured max_input_tokens fallback when the model does not advertise one.
// A learned cap from a provider 400 overflow error overrides the catalog value
// for the provider+model so future turns do not overestimate the window.
// A manual context override (set via the review agent) wins over the learned
// cap — the provider clone already carries it, so we skip the learned cap
// when one is present to avoid clobbering the operator's correction.
func (a *Service) ResolveContextWindow(provider *domain.Provider, model string, settings domain.Settings) int {
	cw := domain.ResolveContextWindow(provider, model, settings)
	if a.learnedParams != nil {
		if cap := a.learnedParams.ContextCap(provider.ID, model); cap > 0 && cap < cw {
			a.log("info", "learning", "capping context window for %s/%s to %d from learned 400", provider.ID, model, cap)
			cw = cap
		}
	}
	// Manual override is applied last and wins over both the catalog value
	// and the learned cap. Applied directly (not just via the mutated clone)
	// so it also covers models absent from the catalog and any resolve path
	// that did not pre-mutate the provider.
	if a.modelOverrides != nil {
		if o := a.modelOverrides.Get(provider.ID, model); o != nil && o.Context != nil {
			cw = *o.Context
		}
	}
	return cw
}

const (
	compactionKeepTokenBudget   = domain.CompactionKeepTokenBudget
	compactionSummaryMaxOut     = domain.CompactionSummaryMaxOut
	compactionSystemReserve     = domain.CompactionSystemReserve
	compactionSummaryMinChars   = domain.CompactionSummaryMinChars
	compactionSummaryMaxRetries = domain.CompactionSummaryMaxRetries
	compactionMaxToolCallChars  = domain.CompactionMaxToolCallChars
)

// compactionSummaryToolName / CompactionSummaryTool live in application/tools
// and are re-exported from tools_wrappers.go. The model calls summary(text="...")
// so the summary is extracted from tool-call arguments, not assistant text.

// compactionToolChoice forces the compaction model to call summary() instead
// of continuing the agent turn as free-text (the failure mode that produced
// one-sentence "handovers" from reasoning models).
//
// The wire shape differs by provider kind:
//   - Anthropic Messages: {"type":"tool","name":"summary"}
//   - OpenAI Responses:   {"type":"function","name":"summary"} (flat — the
//     Responses API rejects the nested Chat shape with
//     "missing_required_parameter: 'tool_choice.name'")
//   - OpenAI Chat / OpenRouter chat: {"type":"function","function":{"name":"summary"}}
func compactionToolChoice(kind domain.ProviderKind) any {
	switch kind {
	case domain.ProviderMessages:
		return map[string]any{"type": "tool", "name": compactionSummaryToolName}
	case domain.ProviderResponses:
		return map[string]any{"type": "function", "name": compactionSummaryToolName}
	default:
		return map[string]any{
			"type":     "function",
			"function": map[string]any{"name": compactionSummaryToolName},
		}
	}
}

// extractCompactionSummary extracts the summary from the model response. It
// prefers the summary() tool call (which carries the text in args, separate
// from reasoning tokens) and falls back to resp.Content for non-tool-calling
// models or when the model ignores the tool and replies as text.
func extractCompactionSummary(resp ChatResponse) string {
	for _, tc := range resp.ToolCalls {
		if tc.Name != compactionSummaryToolName {
			continue
		}
		var parsed struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(tc.Args), &parsed); err == nil && strings.TrimSpace(parsed.Text) != "" {
			return parsed.Text
		}
	}
	return resp.Content
}

// compactionSummaryEchoesAssistant reports whether the candidate summary is
// just a copy of the latest assistant turn (the compaction model continuing
// the live agent instead of writing a handoff).
func compactionSummaryEchoesAssistant(summary string, msgs []ChatMessage) bool {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return false
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" {
			continue
		}
		ast := strings.TrimSpace(msgs[i].Content)
		if len(ast) < 80 {
			return false
		}
		prefix := ast
		if len(prefix) > 120 {
			prefix = prefix[:120]
		}
		return strings.Contains(summary, prefix)
	}
	return false
}

// compactConversation summarizes the conversation history via multi-pass
// rolling compaction so that conversations larger than the model's context
// window are still fully summarized without dropping any messages.
//
// The conversation is split into chunks that fit within the model's context
// window. Each chunk is summarized together with the running summary from the
// previous pass, producing a progressively folded summary that preserves all
// prior context. The most recent messages are kept intact.
//
// Codex remote v2 compaction is a separate streaming /responses request.
// It is selected by provider kind, not by the model ID: Codex model IDs
// also appear in the OpenAI server-compaction table.

func (a *Service) CompactConversation(ctx context.Context, adapter ProviderContext, c *domain.Conversation, model string, contextWindow int, settings domain.Settings, trigger domain.CompactionTrigger) (string, error) {
	if len(c.Messages) <= 1 {
		return "", nil
	}

	effectiveKeepBudget := compactionKeepTokenBudget
	if cap := contextWindow * 3 / 10; cap < effectiveKeepBudget {
		effectiveKeepBudget = cap
	}
	if effectiveKeepBudget < 1000 {
		effectiveKeepBudget = 1000
	}

	if domain.CodexSupportsRemoteCompaction(adapter.Kind) {
		return a.compactCodexConversation(ctx, adapter, c, model, domain.CompactionKeepTokenBudget)
	}

	// Server-side compaction (context_management) is handled by the server
	// during the normal stream call. When the model is eligible, skip the
	// client-side summarization entirely — the server compacts automatically
	// when the threshold is crossed and returns a compaction item in the
	// response stream. The application layer captures and stores it.
	if domain.OpenAISupportsServerCompaction(model) {
		return "", nil
	}

	// Clone a contiguous prefix to summarize. Compact/ArchiveMessages use
	// the same split so keep is a suffix clone — not "all users + recent
	// assistants", which piled users on top of the handoff request.
	splitIdx := c.CompactionSplitIndex(effectiveKeepBudget)

	toCompact := domain.FilterHydrationDomainMessages(c.Messages[:splitIdx])
	runningSummary := c.Summary

	remainingMsgs := make([]domain.Message, 0, len(toCompact))
	for _, m := range toCompact {
		if m.Role == domain.RoleSystem {
			continue
		}
		if domain.IsCompactionSummary(m.Content) {
			continue
		}
		remainingMsgs = append(remainingMsgs, m)
	}
	if len(remainingMsgs) == 0 {
		return "", nil
	}

	systemPrompt := compactionPrompt
	summaryMaxOut := compactionSummaryMaxOut
	if settings.CompactionSummaryMaxTokens > 0 {
		summaryMaxOut = settings.CompactionSummaryMaxTokens
	}
	summaryMinChars := compactionSummaryMinChars
	if settings.CompactionSummaryMinChars > 0 {
		summaryMinChars = settings.CompactionSummaryMinChars
	}
	// Clamp the summary budget to the context window so the doubled retry
	// budget never exceeds what the model can accept. Reserve system overhead
	// and a minimum input floor so the model still has room for the chunk.
	maxBudget := contextWindow - compactionSystemReserve
	if maxBudget < 1000 {
		maxBudget = 1000
	}

	for len(remainingMsgs) > 0 {
		available := compactionPassAvailable(contextWindow, runningSummary, summaryMaxOut)
		chunk, rest := domain.TakeCompactionChunk(remainingMsgs, available)
		remainingMsgs = rest
		if len(chunk) == 0 {
			break
		}
		// Cap each tool call's args/output so a single oversized call still
		// fits the compaction model's window. The cap shrinks with the pass
		// budget so small-window compaction models stay safe too.
		toolCap := compactionMaxToolCallChars
		if a2 := available * 2; a2 < toolCap {
			toolCap = a2
		}
		var msgs []ChatMessage
		if runningSummary != "" {
			msgs = append(msgs, ChatMessage{
				Role:    "user",
				Content: resources.CompactedUserPrompt(runningSummary),
			})
		}
		for _, m := range chunk {
			switch m.Role {
			case domain.RoleUser:
				// Media/file attachments are stripped from the compaction
				// input and replaced with a text note. Compaction models are
				// often not vision/audio-capable, and providers reject the
				// request outright (e.g. OpenRouter HTTP 404 "No endpoints
				// found that support image input") — which made compaction
				// fail and the turn die with a context-overflow 400. The
				// summary only needs to know that content was attached.
				content := m.Content
				if note := compactionAttachmentNote(m); note != "" {
					if content == "" {
						content = note
					} else {
						content = content + "\n\n" + note
					}
				}
				msgs = append(msgs, ChatMessage{Role: "user", Content: content})
			case domain.RoleAssistant:
				if m.Content == "" && len(m.ToolCalls) == 0 {
					continue
				}
				calls := capCompactionToolCalls(m.ToolCalls, toolCap)
				cm := ChatMessage{Role: "assistant", Content: m.Content, Reasoning: m.Reasoning, ToolCalls: calls}
				if len(m.ReasoningExtra) > 0 {
					cm.ReasoningExtra = append(json.RawMessage(nil), m.ReasoningExtra...)
				}
				msgs = append(msgs, cm)
				for _, tc := range calls {
					msgs = append(msgs, ChatMessage{Role: "tool", ToolResult: &ToolResult{
						ToolCallID: tc.ID, Name: tc.Name, Content: tooloutput.ProviderToolContent(tc.Name, tc.Output),
					}})
				}
			}
		}
		msgs = appendCompactionHandoffUser(msgs)
		// Quality guard: retry up to compactionSummaryMaxRetries times when
		// the summary is too short. Each retry doubles the max_output_tokens
		// budget so a reasoning model has more room for content after
		// reasoning. The budget is clamped to the context window so it never
		// exceeds what the model can accept. If all retries fail, return an
		// error so the caller emits EventCompactionFailed and the user cannot
		// continue until compaction succeeds (retry re-enters compaction).
		passBudget := summaryMaxOut
		if passBudget > maxBudget {
			passBudget = maxBudget
		}
		// One pass = one AgentEngine run: summary() is forced via
		// ToolChoice, retries double the token budget until the summary
		// is long enough or the budget is exhausted. On failure, return
		// an error so the caller emits EventCompactionFailed and the user
		// cannot continue until compaction succeeds (retry re-enters
		// compaction).
		pass := &compactionPass{
			svc: a, adapter: adapter, model: model,
			system: systemPrompt, msgs: msgs,
			budget: passBudget, maxBudget: maxBudget, minChars: summaryMinChars,
			convID: c.ID,
		}
		if !pass.run(ctx) {
			return "", fmt.Errorf("compaction failed: summary too short after %d retries (last=%d chars, min=%d): %w",
				compactionSummaryMaxRetries, pass.lastLen, summaryMinChars, pass.lastErr)
		}
		runningSummary = pass.summary
	}

	if err := a.PersistCompactedConversation(c, runningSummary, effectiveKeepBudget); err != nil {
		return "", err
	}
	return runningSummary, nil
}

func (a *Service) compactCodexConversation(ctx context.Context, adapter ProviderContext, c *domain.Conversation, model string, keepBudget int) (string, error) {
	caps := ModelCapabilities{Vision: true, Audio: true, Video: true, Document: true}
	messages := a.chatMessagesForProvider(c, "", caps)
	request := func(active ProviderContext) (ChatResponse, error) {
		return active.Stream(ctx, ChatRequest{
			Model:                    model,
			Messages:                 messages,
			ConversationID:           c.ID,
			CompactionBlob:           c.CompactionBlob,
			CompactionPrefixMessages: a.compactionPrefixMessageCount(c, caps),
			RemoteCompaction:         true,
			ReasoningReplay:          true,
		}, nil, nil)
	}

	// Re-select the account immediately before the separate remote-compaction
	// request. The chat adapter was created before the prior round, so its
	// token can have become circuit-open after a usage-limit response.
	provider, apiKey, active, err := a.prepareCodexCompactionAdapter(ctx, adapter, c.ID)
	if err != nil {
		return "", err
	}
	response, err := request(active)
	if err != nil && provider != nil && a.deps.FailoverOnStreamError != nil {
		newKey, retry, replaced := a.deps.FailoverOnStreamError(ctx, c.ID, provider, apiKey, err)
		if replaced != nil {
			return "", replaced
		}
		if retry && newKey != "" {
			if a.Factory == nil {
				return "", fmt.Errorf("rebuild Codex compaction adapter after failover: provider factory unavailable")
			}
			rebuilt, buildErr := a.Factory(ctx, provider, newKey)
			if buildErr != nil {
				return "", fmt.Errorf("rebuild Codex compaction adapter after failover: %w", buildErr)
			}
			response, err = request(NewProviderContext(provider, rebuilt))
		}
	}
	if err != nil {
		return "", fmt.Errorf("codex remote compaction: %w", err)
	}
	if len(response.CompactionItems) != 1 {
		return "", fmt.Errorf("codex remote compaction returned %d compaction items, want exactly one", len(response.CompactionItems))
	}
	blob, err := json.Marshal(response.CompactionItems)
	if err != nil {
		return "", fmt.Errorf("marshal Codex compaction item: %w", err)
	}
	if err := a.PersistCodexCompactedConversation(c, string(blob), keepBudget); err != nil {
		return "", err
	}
	return "", nil
}

// prepareCodexCompactionAdapter reuses the turn's adapter unless the account
// router selects a fresh credential. It keeps a no-router/single-key Codex
// provider on its existing adapter, rather than rebuilding it with an empty
// key. The account is selected immediately before compaction because this is
// a separate request from the preceding model stream.
func (a *Service) prepareCodexCompactionAdapter(ctx context.Context, adapter ProviderContext, conversationID string) (*domain.Provider, string, ProviderContext, error) {
	if a == nil || adapter.Kind != domain.ProviderCodex || a.Providers == nil || a.Factory == nil || a.deps.PrepareTurnAPIKey == nil {
		return nil, "", adapter, nil
	}
	provider, err := a.Providers.Get(adapter.ProviderID)
	if err != nil || provider == nil {
		return nil, "", adapter, nil
	}
	apiKey, err := a.deps.PrepareTurnAPIKey(conversationID, provider, "")
	if err != nil {
		return provider, "", adapter, fmt.Errorf("select Codex account for remote compaction: %w", err)
	}
	if apiKey == "" {
		return provider, "", adapter, nil
	}
	rebuilt, err := a.Factory(ctx, provider, apiKey)
	if err != nil {
		return provider, apiKey, adapter, fmt.Errorf("build Codex compaction adapter: %w", err)
	}
	return provider, apiKey, NewProviderContext(provider, rebuilt), nil
}

// compactionAttachmentNote renders a short text note for message attachments
// so the compaction summary knows media/files were attached without
// receiving their bytes (see the stripping comment in compactConversation).
func compactionAttachmentNote(m domain.Message) string {
	if len(m.Attachments) == 0 {
		return ""
	}
	names := make([]string, 0, len(m.Attachments))
	for _, att := range m.Attachments {
		label := att.Name
		if label == "" {
			label = att.Type
		}
		if label == "" {
			label = "attachment"
		}
		if att.Type != "" && !strings.Contains(label, att.Type) {
			label += " (" + att.Type + ")"
		}
		names = append(names, label)
	}
	return "[attachments: " + strings.Join(names, ", ") + "]"
}

// capCompactionToolCalls returns a copy of the tool calls whose args and
// output are truncated to capChars with an omission marker, so one oversized
// call (e.g. a grep over huge lines, a 10MB file_write content) cannot exceed
// the compaction model's context window.
func capCompactionToolCalls(calls []domain.ToolCall, capChars int) []domain.ToolCall {
	if capChars <= 0 {
		return calls
	}
	out := make([]domain.ToolCall, 0, len(calls))
	for _, tc := range calls {
		if len(tc.Args) > capChars {
			tc.Args = text.TruncateRunes(tc.Args, capChars)
		}
		if len(tc.Output) > capChars {
			tc.Output = text.TruncateRunes(tc.Output, capChars)
		}
		out = append(out, tc)
	}
	return out
}

// compactionPassAvailable is the per-pass token budget for message content.
// The running summary grows across passes, so later chunks shrink to leave
// room for it instead of using a one-shot 2000-token reserve.
func compactionPassAvailable(contextWindow int, runningSummary string, summaryMaxOut int) int {
	summaryTokens := domain.EstimateTokens(runningSummary)
	if runningSummary != "" {
		summaryTokens += domain.EstimateTokens(resources.CompactedUserPrompt(""))
	}
	handoffTokens := domain.EstimateTokens(strings.TrimSpace(compactionHandoffUserPrompt))
	available := contextWindow - compactionSystemReserve - summaryTokens - summaryMaxOut - handoffTokens
	if available < 1000 {
		return 1000
	}
	return available
}

// appendCompactionHandoffUser appends the handoff command as the last user
// message so the compaction model is not left on an assistant/tool turn.
func appendCompactionHandoffUser(msgs []ChatMessage) []ChatMessage {
	closer := strings.TrimSpace(compactionHandoffUserPrompt)
	if closer == "" {
		return msgs
	}
	return append(msgs, ChatMessage{Role: "user", Content: closer})
}

func (a *Service) PersistCompactedConversation(c *domain.Conversation, summary string, keepBudget int) error {
	if c == nil {
		return fmt.Errorf("conversation is required")
	}
	repo := bindConversation(a.Conversations, c)
	tmp := cloneConversation(c)
	tmp.Messages = domain.FilterHydrationDomainMessages(tmp.Messages)
	toArchive := tmp.ArchiveMessages(keepBudget)
	if len(toArchive) > 0 {
		idx, err := a.Conversations.ArchiveChunk(c.ID, toArchive)
		if err != nil {
			return fmt.Errorf("archive compaction chunk for %s: %w", c.ID, err)
		}
		c.ChunkCount = idx + 1
	}
	tmp.Summary = ""
	handoverContent := resources.CompactedUserPrompt(summary)
	tmp.Compact(summary, handoverContent, keepBudget)
	if hydrationMsgs := a.BuildHydration(tmp); len(hydrationMsgs) > 0 {
		tmp = a.PersistHydration(tmp, hydrationMsgs)
	}
	chunkCount := c.ChunkCount
	workspace := c.Workspace
	model := c.Model
	effort := c.Effort
	origin := c.Origin
	title := c.Title
	status := c.Status
	pending := c.PendingWorkspaceAnnouncement
	switchFrom := c.WorkspaceSwitchFrom
	blob := tmp.CompactionBlob
	epochSummary := tmp.Summary
	repo.ResetTranscript()
	c.ChunkCount = chunkCount
	c.Workspace = workspace
	c.Model = model
	c.Effort = effort
	c.Origin = origin
	c.Title = title
	c.Status = status
	c.PendingWorkspaceAnnouncement = pending
	c.WorkspaceSwitchFrom = switchFrom
	c.CompactionBlob = blob
	c.Summary = epochSummary
	for _, m := range tmp.Messages {
		if err := repo.Add(m.Role, m); err != nil {
			return err
		}
	}
	return repo.Save()
}

// PersistCodexCompactedConversation persists a Codex opaque checkpoint as a
// new transcript epoch. Unlike text compaction, this intentionally does not
// add a user handover message: the provider-owned blob carries the archived
// context and only the retained suffix belongs in the live transcript.
func (a *Service) PersistCodexCompactedConversation(c *domain.Conversation, blob string, keepBudget int) error {
	if c == nil {
		return fmt.Errorf("conversation is required")
	}
	repo := bindConversation(a.Conversations, c)
	tmp := cloneConversation(c)
	tmp.Messages = domain.FilterHydrationDomainMessages(tmp.Messages)
	toArchive := tmp.ArchiveBlobMessages(keepBudget)
	if len(toArchive) > 0 {
		idx, err := a.Conversations.ArchiveChunk(c.ID, toArchive)
		if err != nil {
			return fmt.Errorf("archive Codex compaction chunk for %s: %w", c.ID, err)
		}
		c.ChunkCount = idx + 1
	}
	tmp.CompactWithBlob(blob, keepBudget)
	if hydrationMsgs := buildHydrationDomainMessages(a.BuildHydration(tmp)); len(hydrationMsgs) > 0 {
		tmp.Messages = append(tmp.Messages, hydrationMsgs...)
	}

	chunkCount := c.ChunkCount
	workspace := c.Workspace
	model := c.Model
	effort := c.Effort
	origin := c.Origin
	title := c.Title
	status := c.Status
	pending := c.PendingWorkspaceAnnouncement
	switchFrom := c.WorkspaceSwitchFrom
	compactionBlob := tmp.CompactionBlob
	compactionPrefixMessages := tmp.CompactionPrefixMessages
	summary := tmp.Summary
	repo.ResetTranscript()
	c.ChunkCount = chunkCount
	c.Workspace = workspace
	c.Model = model
	c.Effort = effort
	c.Origin = origin
	c.Title = title
	c.Status = status
	c.PendingWorkspaceAnnouncement = pending
	c.WorkspaceSwitchFrom = switchFrom
	c.CompactionBlob = compactionBlob
	c.CompactionPrefixMessages = compactionPrefixMessages
	c.Summary = summary
	for _, m := range tmp.Messages {
		if err := repo.Add(m.Role, m); err != nil {
			return err
		}
	}
	return repo.Save()
}
