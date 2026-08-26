package openai

import (
	"net/url"
	"strings"

	"nusashell/infrastructure/ai/core"
)

// promptCacheParamsSupport reports whether this endpoint is trusted to accept
// OpenAI's prompt cache params.
// Only the official endpoint guarantees the field contract; see
// Config.PromptCacheParams for the opt-in on compatible backends.
func (p *Provider) promptCacheParamsSupport() core.Support {
	if p.cfg.PromptCacheParams || isOfficialBaseURL(p.cfg.BaseURL) {
		return core.SupportYes
	}
	return core.SupportUnknown
}

func isOfficialBaseURL(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Hostname(), "api.openai.com")
}

// structuredSupport reports the endpoint contract implemented by this
// provider. The official OpenAI Chat/Responses APIs accept Structured Outputs;
// a compatible custom endpoint makes no such guarantee and remains Unknown.
// Model-specific exceptions are enforced by the endpoint, not an ever-growing
// model-name list here.
func (p *Provider) structuredSupport() core.StructuredCapabilities {
	if !isOfficialBaseURL(p.cfg.BaseURL) {
		return core.StructuredCapabilities{
			JSONObject: core.SupportUnknown,
			JSONSchema: core.SupportUnknown,
			Strict:     core.SupportUnknown,
		}
	}
	return core.StructuredCapabilities{
		JSONObject: core.SupportYes,
		JSONSchema: core.SupportYes,
		Strict:     core.SupportYes,
	}
}

func (p *Provider) Capabilities(model string) core.Capabilities {
	thinking := core.ThinkingCapabilities{
		Supported: core.SupportPartial,
		Disable:   core.SupportPartial,
		Efforts:   openAIReasoningEfforts(),
		Notes:     []string{"chat reasoning controls are available on reasoning chat models; model-specific limits are enforced by the OpenAI API"},
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
			StrictSchema:        core.SupportYes,
			Choice:              core.SupportYes,
			HostedProviderTools: core.SupportPartial,
		},
		Structured: p.structuredSupport(),
		Media: core.MediaCapabilities{
			ImageURL:    core.SupportYes,
			ImageBytes:  core.SupportYes,
			FileURI:     core.SupportNo,
			ImageDetail: core.SupportYes,
		},
		Cache: core.CacheCapabilities{
			Block:      p.promptCacheParamsSupport(),
			PromptKey:  p.promptCacheParamsSupport(),
			Retention:  p.promptCacheParamsSupport(),
			UsageRead:  core.SupportYes,
			UsageWrite: core.SupportPartial,
		},
		Streaming: core.StreamingCapabilities{
			Supported:       core.SupportYes,
			Usage:           core.SupportYes,
			ReasoningDeltas: core.SupportYes,
			ToolCallDeltas:  core.SupportYes,
			NativeResponses: core.SupportYes,
			IdleTimeout:     core.SupportYes,
		},
		Usage: core.UsageCapabilities{
			InputTokens:      core.SupportYes,
			OutputTokens:     core.SupportYes,
			TotalTokens:      core.SupportYes,
			ReasoningTokens:  core.SupportYes,
			CacheReadTokens:  core.SupportYes,
			CacheWriteTokens: core.SupportPartial,
		},
	}
}
