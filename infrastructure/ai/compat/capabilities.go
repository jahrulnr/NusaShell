package compat

import "nusashell/infrastructure/ai/core"

func (s Spec) defaultCapabilities(provider, model string) core.Capabilities {
	caps := core.Capabilities{
		Provider: provider,
		Model:    model,
		Thinking: core.ThinkingCapabilities{
			Supported: core.SupportNo,
			Disable:   core.SupportNo,
		},
		Reasoning: core.ReasoningCapabilities{
			Blocks:          supportFromBool(len(s.Response.ReasoningFields) > 0),
			StreamingDeltas: supportFromBool(len(s.Response.ReasoningFields) > 0 || len(s.Stream.ReasoningFields) > 0),
			ReasoningTokens: supportFromBool(s.Response.HasCompletionTokenDetails),
		},
		Tools: core.ToolCapabilities{
			Calls:         core.SupportYes,
			ParallelCalls: core.SupportUnknown,
			StrictSchema:  strictToolSupport(s.Features.StrictTools),
			Choice:        core.SupportYes,
		},
		Structured: core.StructuredCapabilities{
			JSONObject: core.SupportYes,
			JSONSchema: supportFromBool(s.Request.SupportsJSONSchema),
			Strict:     strictJSONSchemaSupport(s.Request),
			PromptOnly: s.Request.JSONSchemaToPrompt,
		},
		Streaming: core.StreamingCapabilities{
			Supported:       core.SupportYes,
			Usage:           supportFromBool(!s.Stream.OmitStreamOptions),
			ReasoningDeltas: supportFromBool(len(s.Response.ReasoningFields) > 0 || len(s.Stream.ReasoningFields) > 0),
			ToolCallDeltas:  core.SupportYes,
			IdleTimeout:     core.SupportYes,
		},
		Usage: core.UsageCapabilities{
			InputTokens:      core.SupportYes,
			OutputTokens:     core.SupportYes,
			TotalTokens:      core.SupportYes,
			ReasoningTokens:  supportFromBool(s.Response.HasCompletionTokenDetails),
			CacheReadTokens:  cacheReadSupport(s.Response),
			CacheWriteTokens: cacheWriteSupport(s.Response),
		},
	}
	if s.Request.Thinking != nil {
		caps.Thinking = core.ThinkingCapabilities{
			Supported: core.SupportYes,
			Disable:   core.SupportYes,
		}
	}
	return caps
}

func cacheReadSupport(spec ResponseSpec) core.Support {
	if spec.HasCacheTokens {
		return core.SupportYes
	}
	if spec.HasCompletionTokenDetails {
		return core.SupportPartial
	}
	return core.SupportNo
}

func cacheWriteSupport(spec ResponseSpec) core.Support {
	if spec.HasCompletionTokenDetails {
		return core.SupportPartial
	}
	return core.SupportNo
}

func supportFromBool(ok bool) core.Support {
	if ok {
		return core.SupportYes
	}
	return core.SupportNo
}

func strictToolSupport(mode StrictToolMode) core.Support {
	switch mode {
	case StrictToolsForward, StrictToolsAlways:
		return core.SupportYes
	case StrictToolsRequireAll:
		return core.SupportPartial
	default:
		return core.SupportNo
	}
}

func strictJSONSchemaSupport(spec RequestSpec) core.Support {
	if !spec.SupportsJSONSchema {
		return core.SupportNo
	}
	return core.SupportPartial
}
