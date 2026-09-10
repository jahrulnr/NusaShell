package gemini

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"nusashell/infrastructure/ai/core"
)

// convertResponse maps a generateContent response onto the core response
// model. Ported from litellm's _process_candidates / transform_response. The
// returned ModelVersion is preferred over the request model when present,
// matching the streaming path so both report the same resolved model.
func convertResponse(resp *generateContentResponse, model string) (*core.Response, error) {
	resolvedModel := model
	if resp.ModelVersion != "" {
		resolvedModel = normalizeModelID(resp.ModelVersion)
	}
	out := &core.Response{
		Model:    resolvedModel,
		Provider: providerName,
		Usage:    convertUsage(resp.UsageMetadata),
	}
	if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != "" {
		// The prompt itself was blocked, so no candidate exists.
		out.FinishReason = core.FinishReasonSafety
		out.FinishReasonRaw = resp.PromptFeedback.BlockReason
		out.Warnings = append(out.Warnings, warning("gemini.prompt_blocked", fmt.Sprintf("prompt blocked by Gemini (%s)", resp.PromptFeedback.BlockReason)))
		return out, nil
	}
	if len(resp.Candidates) == 0 {
		return nil, core.NewProviderError(providerName, core.ErrorTypeProvider, "gemini: response contained no candidates")
	}
	candidate := resp.Candidates[0]
	if candidate.Content != nil {
		blocks, err := convertParts(candidate.Content.Parts)
		if err != nil {
			return nil, err
		}
		out.Blocks = blocks
	}
	finish := core.NormalizeFinishReason(candidate.FinishReason)
	if len(out.ToolCalls()) > 0 && (finish == core.FinishReasonStop || finish == "") {
		// A turn that produced tool calls stops for tool use, not for
		// completion, even when Gemini reports STOP.
		finish = core.FinishReasonToolCall
	}
	out.FinishReason = finish
	out.FinishReasonRaw = candidate.FinishReason
	return out, nil
}

func convertParts(parts []part) ([]core.Block, error) {
	blocks := make([]core.Block, 0, len(parts))
	for _, p := range parts {
		switch {
		case p.FunctionCall != nil:
			args := p.FunctionCall.Args
			if len(args) == 0 || !json.Valid(args) {
				args = json.RawMessage("{}")
			}
			id := strings.TrimSpace(p.FunctionCall.ID)
			if id == "" {
				id = newToolCallID()
			}
			blocks = append(blocks, core.ToolUseBlock{
				ID:        id,
				Name:      p.FunctionCall.Name,
				Arguments: args,
				// The signature travels with the call: Gemini 3 rejects a
				// replayed functionCall part without it.
				Signature: p.ThoughtSignature,
			})
		case p.FunctionResponse != nil:
			// Server-side (context circulation) tool responses echo history
			// back; they are not assistant output.
		case p.Thought:
			if p.Text == "" && p.ThoughtSignature == "" {
				continue
			}
			blocks = append(blocks, core.ReasoningBlock{
				Text:      p.Text,
				Signature: p.ThoughtSignature,
				Extra:     signatureExtra(p.ThoughtSignature),
			})
		default:
			if p.ThoughtSignature != "" {
				// A signature returned on a text part must survive the next
				// turn: it is carried as opaque reasoning state, which the
				// application persists and replays.
				blocks = append(blocks, core.ReasoningBlock{
					Signature: p.ThoughtSignature,
					Extra:     signatureExtra(p.ThoughtSignature),
				})
			}
			if p.Text != "" {
				blocks = append(blocks, core.TextBlock{Text: p.Text})
			}
			if p.InlineData != nil {
				block, err := convertInlineData(p.InlineData)
				if err != nil {
					return nil, err
				}
				if block != nil {
					blocks = append(blocks, block)
				}
			}
		}
	}
	return blocks, nil
}

func convertInlineData(data *blob) (core.Block, error) {
	if data == nil || data.Data == "" {
		return nil, nil
	}
	switch {
	case strings.HasPrefix(data.MimeType, "image/"):
		decoded, err := base64.StdEncoding.DecodeString(data.Data)
		if err != nil {
			return nil, fmt.Errorf("gemini: decode inline image: %w", err)
		}
		return core.ImageBlock{Data: decoded, MIME: data.MimeType}, nil
	case strings.HasPrefix(data.MimeType, "audio/"):
		decoded, err := base64.StdEncoding.DecodeString(data.Data)
		if err != nil {
			return nil, fmt.Errorf("gemini: decode inline audio: %w", err)
		}
		return core.AudioBlock{Data: decoded, MIME: data.MimeType}, nil
	case strings.HasPrefix(data.MimeType, "video/"):
		return core.VideoBlock{
			URL:  "data:" + data.MimeType + ";base64," + data.Data,
			MIME: data.MimeType,
		}, nil
	default:
		return nil, nil
	}
}

// convertUsage maps usageMetadata onto core usage. Ported from litellm's
// _calculate_usage: thought tokens are added to the completion count when the
// provider reports them outside candidatesTokenCount, and tool-use prompt
// tokens are billed as input.
func convertUsage(meta *usageMetadata) core.Usage {
	if meta == nil {
		return core.Usage{}
	}
	out := core.Usage{
		InputTokens:     meta.PromptTokenCount + meta.ToolUsePromptTokenCount,
		OutputTokens:    meta.CandidatesTokenCount,
		TotalTokens:     meta.TotalTokenCount,
		ReasoningTokens: meta.ThoughtsTokenCount,
		CacheReadTokens: meta.CachedContentTokenCount,
		Provider:        providerName,
	}
	if meta.ThoughtsTokenCount > 0 && !candidateCountIncludesThoughts(meta) {
		out.OutputTokens += meta.ThoughtsTokenCount
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.InputTokens + out.OutputTokens
	}
	return out
}

// candidateCountIncludesThoughts mirrors litellm's
// is_candidate_token_count_inclusive: when the parts already add up to the
// reported total, candidatesTokenCount already contains the thinking tokens.
func candidateCountIncludesThoughts(meta *usageMetadata) bool {
	if meta == nil || meta.TotalTokenCount == 0 {
		return true
	}
	return meta.PromptTokenCount+meta.CandidatesTokenCount+meta.ToolUsePromptTokenCount == meta.TotalTokenCount
}

// newToolCallID synthesizes a stable-width tool call id. Gemini only returns
// native ids for strict matching on newer models; older models need an id so
// tool results can reference the call.
func newToolCallID() string {
	var buf [14]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "call_0000000000000000000000000000"
	}
	return "call_" + hex.EncodeToString(buf[:])
}
