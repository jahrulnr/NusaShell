package gemini

import "nusashell/infrastructure/ai/core"

// Capabilities reports what the Gemini wire can express for a model. Thinking
// control is model-generation specific: Gemini 2.x and older take a token
// budget, Gemini 3 takes a level.
func (p *Provider) Capabilities(model string) core.Capabilities {
	thinking := core.ThinkingCapabilities{
		Supported:     core.SupportYes,
		Disable:       core.SupportYes,
		Efforts:       thinkingEffortsFor(model),
		IncludeOutput: core.SupportYes,
		BudgetTokens:  core.SupportYes,
	}
	if isGemini3(model) {
		thinking.BudgetTokens = core.SupportNo
		thinking.Notes = []string{
			"Gemini 3 uses thinking levels instead of budgets; a requested budget is replaced by the matching level",
			"disable maps to the lowest supported level because Gemini 3 cannot fully disable thinking",
			"unsupported levels are clamped to the nearest supported level and reported as a warning",
		}
	} else {
		thinking.Notes = []string{
			"reasoning_effort maps onto a thinking budget: minimal 1-512, low 1024, medium 2048, high 4096 tokens",
			"thinking is disabled with a zero budget",
		}
	}
	return core.Capabilities{
		Provider: p.Name(),
		Model:    model,
		Thinking: thinking,
		Reasoning: core.ReasoningCapabilities{
			Blocks:          core.SupportYes,
			StreamingDeltas: core.SupportYes,
			ReasoningTokens: core.SupportYes,
		},
		Tools: core.ToolCapabilities{
			Calls:               core.SupportYes,
			ParallelCalls:       core.SupportYes,
			StrictSchema:        core.SupportNo,
			Choice:              core.SupportYes,
			MultimodalResults:   core.SupportYes,
			RequiresAdjacency:   true,
			RoundTripSignatures: core.SupportYes,
			HostedProviderTools: core.SupportNo,
		},
		Structured: core.StructuredCapabilities{
			JSONObject: core.SupportYes,
			JSONSchema: core.SupportYes,
			Strict:     core.SupportNo,
		},
		Media: core.MediaCapabilities{
			ImageURL:   core.SupportYes,
			ImageBytes: core.SupportYes,
			FileURI:    core.SupportYes,
		},
		Cache: core.CacheCapabilities{
			// Prompt caching is implicit server-side; there is no cache block
			// or retention control, but cached tokens are reported.
			Block:      core.SupportNo,
			UsageRead:  core.SupportYes,
			UsageWrite: core.SupportNo,
		},
		Streaming: core.StreamingCapabilities{
			Supported:       core.SupportYes,
			Usage:           core.SupportYes,
			ReasoningDeltas: core.SupportYes,
			ToolCallDeltas:  core.SupportYes,
			IdleTimeout:     core.SupportYes,
		},
		Usage: core.UsageCapabilities{
			InputTokens:      core.SupportYes,
			OutputTokens:     core.SupportYes,
			TotalTokens:      core.SupportYes,
			ReasoningTokens:  core.SupportYes,
			CacheReadTokens:  core.SupportYes,
			CacheWriteTokens: core.SupportNo,
		},
	}
}

func thinkingEffortsFor(model string) []string {
	// The portable effort names map onto budgets (2.x) or levels (3+).
	// Gemini 3 returns only the levels the model can actually represent so
	// the UI does not offer unsupported levels.
	if isGemini3(model) {
		return gemini3ThinkingLevels(model)
	}
	return []string{"minimal", "low", "medium", "high"}
}
