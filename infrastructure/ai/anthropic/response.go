package anthropic

import (
	"encoding/json"
	"fmt"

	"nusashell/infrastructure/ai/core"
)

type anthropicResponse struct {
	Content    []anthropicContent `json:"content"`
	Usage      anthropicUsage     `json:"usage"`
	Model      string             `json:"model"`
	StopReason string             `json:"stop_reason"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

func convertResponse(resp *anthropicResponse, fallbackModel string) (*core.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("anthropic: response cannot be nil")
	}
	out := &core.Response{
		Model:        resp.Model,
		Provider:     "anthropic",
		FinishReason: core.NormalizeFinishReason(resp.StopReason),
		Usage: core.Usage{
			InputTokens:      resp.Usage.InputTokens + resp.Usage.CacheReadInputTokens,
			OutputTokens:     resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.CacheReadInputTokens + resp.Usage.OutputTokens,
			CacheReadTokens:  resp.Usage.CacheReadInputTokens,
			CacheWriteTokens: resp.Usage.CacheCreationInputTokens,
			Provider:         "anthropic",
		},
	}
	if out.Model == "" {
		out.Model = fallbackModel
	}
	out.Usage.Model = out.Model
	for _, content := range resp.Content {
		switch content.Type {
		case "text":
			out.Blocks = append(out.Blocks, core.TextBlock{Text: content.Text})
		case "thinking":
			out.Blocks = append(out.Blocks, core.ReasoningBlock{Text: content.Thinking, Signature: content.Signature})
		case "redacted_thinking":
			out.Blocks = append(out.Blocks, core.ReasoningBlock{Redacted: append([]byte(nil), content.Data...)})
		case "tool_use":
			args, err := json.Marshal(content.Input)
			if err != nil {
				return nil, fmt.Errorf("anthropic: marshal tool use %q arguments: %w", content.Name, err)
			}
			out.Blocks = append(out.Blocks, core.ToolUseBlock{
				ID:        content.ID,
				Name:      content.Name,
				Arguments: args,
			})
		default:
			// Beta features (server tools, compaction, fallback blocks, ...)
			// can add block types this provider does not model; keep the
			// response usable and surface the drop instead of failing.
			out.Warnings = append(out.Warnings, warning("anthropic.unsupported_content_block",
				fmt.Sprintf("dropped unsupported response content block %q", content.Type)))
		}
	}
	return out, nil
}
