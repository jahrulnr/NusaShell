package anthropic

import "nusashell/infrastructure/ai/core"

func (p *Provider) Capabilities(model string) core.Capabilities {
	caps := core.Capabilities{
		Provider: p.Name(),
		Model:    model,
		Thinking: core.ThinkingCapabilities{
			Supported:     core.SupportYes,
			Disable:       core.SupportUnknown,
			Efforts:       []string{"low", "medium", "high"},
			BudgetTokens:  core.SupportNo,
			IncludeOutput: core.SupportYes,
			Notes:         []string{"adaptive thinking baseline; disable, xhigh, and max support are model-specific"},
		},
		Reasoning: core.ReasoningCapabilities{
			Blocks:          core.SupportYes,
			StreamingDeltas: core.SupportYes,
			ReasoningTokens: core.SupportNo,
		},
		Tools: core.ToolCapabilities{
			Calls:               core.SupportYes,
			StrictSchema:        core.SupportYes,
			Choice:              core.SupportPartial,
			MultimodalResults:   core.SupportYes,
			RoundTripSignatures: core.SupportYes,
		},
		Structured: core.StructuredCapabilities{
			JSONObject: core.SupportNo,
			JSONSchema: core.SupportUnknown,
			Strict:     core.SupportYes,
		},
		Media: core.MediaCapabilities{
			ImageURL:   core.SupportYes,
			ImageBytes: core.SupportYes,
			FileURI:    core.SupportNo,
		},
		Cache: core.CacheCapabilities{
			Block:      core.SupportYes,
			Retention:  core.SupportYes,
			UsageRead:  core.SupportYes,
			UsageWrite: core.SupportYes,
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
			ReasoningTokens:  core.SupportNo,
			CacheReadTokens:  core.SupportYes,
			CacheWriteTokens: core.SupportYes,
		},
	}
	return caps
}
