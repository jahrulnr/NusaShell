package application

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"nusashell/domain"
)

// runImprover runs the background improver for one conversation: a hidden
// pipeline turn with AgentImprover tooling (the full base toolbox minus
// ACP/delegate tools, plus review_transcript and model_override) that
// studies the conversation's real evidence and writes durable memory
// (soul.md + fragments) through the real `memory` tool. Mutations are
// announced to every other conversation by the shared executeTurnTools
// fan-out — the improver does not need its own announcement path.
//
// The prompt (resources/agent/prompts/improve.md) teaches the improver to
// read the transcript JSON directly, inspect the files the conversation
// touched via file_*/grep/exec, optionally research the web, and obey the
// guardrails (no delete, no writes to the user tier, cap on mutations).
func (a *App) runImprover(ctx context.Context, conversationID string) error {
	if a.Conversations == nil {
		return fmt.Errorf("improver: conversation store not configured")
	}
	conv, err := a.Conversations.Get(conversationID)
	if err != nil {
		return fmt.Errorf("improver: %w", err)
	}
	if conv == nil {
		return fmt.Errorf("improver: conversation %s not found", conversationID)
	}
	// Prefer the conversation's own provider+model; the headless turn
	// falls back to the default provider when nothing resolves.
	modelID := ""
	if p, m, _, _, perr := a.resolveConversationProvider(conv); perr == nil {
		modelID = p.ID + ":" + m
	}
	prompt := a.improverPrompt(conv)
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("improver: prompt resource empty")
	}
	if _, _, err := a.runHeadlessTurnKindObserved(ctx, prompt, modelID, domain.TrustTrusted, nil, AgentImprover, nil); err != nil {
		return fmt.Errorf("improver: %w", err)
	}
	return nil
}

// improverPromptBuilds injects the conversation JSON path and workspace
// into the improve.md prompt body.
func (a *App) improverPrompt(conv *domain.Conversation) string {
	p := improvePrompt
	p = strings.ReplaceAll(p, "{{conversation_path}}", conversationJSONPath(a.DataDir, conv.ID))
	p = strings.ReplaceAll(p, "{{workspace}}", conv.Workspace)
	return p
}

// conversationJSONPath returns the absolute path of a conversation's JSON
// file in the data directory layout (conversations/<id>.json). Empty when
// DataDir is not configured.
func conversationJSONPath(dataDir, conversationID string) string {
	if strings.TrimSpace(dataDir) == "" || conversationID == "" {
		return ""
	}
	return filepath.Join(dataDir, "conversations", conversationID+".json")
}
