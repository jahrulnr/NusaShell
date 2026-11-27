package appconversation

import (
	"context"

	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/prompt"
)

// ContextFooterStats is the estimated live input size and the model's input
// allowance for the context footer (~estimate/total), aligned with compaction.
// A room bound to an agent has no estimate: the agent keeps its own context
// and reports it, so Agent holds that report and Total is its window.
type ContextFooterStats struct {
	Estimate int
	Total    int
	Agent    valueobject.AgentContext
}

// ContextFooterStats loads the conversation's estimated input tokens and
// resolves the input allowance from the bound provider/model. A new room
// (empty or not-yet-persisted convID) still answers the capacity half
// when providerID/modelID name a known model — the footer shows "~/192K"
// before the first send. A room bound to an agent, with no other provider
// picked for its next turn, answers with the agent's own report instead.
func (app *ConversationApp) ContextFooterStats(ctx context.Context, convID, providerID, modelID string) (ContextFooterStats, error) {
	deps := app.depsSnapshot()
	var stats ContextFooterStats
	if convID != "" {
		conv, err := deps.Convs.Load(ctx, convID)
		if err != nil {
			if apperror.As(err).StatusCode() != apperror.StatusNotFound {
				return stats, err
			}
		} else if !conv.Runtime().HarnessOwnsContext() && providerID == "" {
			stats.Agent = conv.AgentContext()
			stats.Total = stats.Agent.Size()
			return stats, nil
		} else {
			// Mirror send/runTurn: system prompt and tool catalog are code
			// applied every request, not restored from the conversation
			// record.
			if conv.SystemPrompt() == "" {
				conv.SetSystemPrompt(prompt.SystemPrompt("conversation"))
			}
			driver := deps.Environments[conv.Environment()]
			if driver == nil {
				return stats, apperror.New(400, "conversation environment unavailable", nil)
			}
			conv.SetTools(driver.ToolSchemas())
			stats.Estimate = conv.EstimatedTokens()
			if providerID == "" {
				providerID = conv.ProviderID()
			}
			if modelID == "" {
				modelID = conv.ModelID()
			}
		}
	}
	if providerID == "" || modelID == "" {
		return stats, nil
	}
	prov, err := deps.Providers.Load(ctx, providerID)
	if err != nil {
		return stats, nil
	}
	model, ok := prov.Model(modelID)
	if !ok {
		return stats, nil
	}
	stats.Total = model.CompactionInputAllowance()
	return stats, nil
}
