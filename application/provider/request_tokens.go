package provider

import (
	"encoding/json"
	"math"
	"strings"
	"unicode/utf8"

	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

const (
	// requestTokenMediaCost is deliberately a modality cost, not a byte cost.
	// Providers tokenize image/audio/video inputs after normalizing or
	// downscaling them; counting an inline base64 data URL as text can turn a
	// one-megabyte image into hundreds of thousands of apparent tokens.
	requestTokenMediaCost = int64(domain.RequestTokenImageCost)
	requestTokenMinImage  = int64(85)
	requestTokenMaxImage  = int64(2000)
)

// EstimateRequestTokens returns a conservative preflight estimate for one
// provider request. It first converts the application request through the
// same provider boundary used for transmission, then estimates the resulting
// model-visible blocks. This is intentionally not presented as provider
// usage: exact context fill is only available from the provider response.
//
// The kind/openRouter arguments matter because reasoning replay and provider
// options are not accepted by every wire format. Estimating after
// ToCoreRequest keeps this number aligned with what each adapter can actually
// serialize and prevents application-only fields from inflating the count.
func EstimateRequestTokens(req ChatRequest, kind domain.ProviderKind, openRouter bool) int64 {
	coreReq := ToCoreRequest(req, kind, openRouter)
	var tokens int64
	for _, message := range coreReq.Messages {
		tokens += estimateCoreMessageTokens(message)
	}
	for _, tool := range coreReq.Tools {
		tokens += estimateCoreToolTokens(tool)
	}
	// Compaction items are opaque provider-visible input for Responses and
	// Codex. Other wire kinds intentionally ignore the option in ToCoreRequest.
	if req.CompactionBlob != "" && (kind == domain.ProviderResponses || kind == domain.ProviderCodex) {
		tokens += estimateOpaqueJSONTokens([]byte(req.CompactionBlob))
	}
	if tokens <= 0 {
		return 0
	}
	return applyRequestTokenSafetyBuffer(tokens)
}

func estimateCoreMessageTokens(message core.Message) int64 {
	tokens := int64(domain.RequestTokenPerMessageOverhead)
	for _, block := range message.Blocks {
		tokens += estimateCoreBlockTokens(block)
	}
	return tokens
}

func estimateCoreBlockTokens(block core.Block) int64 {
	switch b := block.(type) {
	case core.TextBlock:
		return estimateTextTokens(b.Text)
	case core.ImageBlock:
		return estimateImageBlockTokens(b)
	case core.AudioBlock:
		return requestTokenMediaCost
	case core.VideoBlock:
		return requestTokenMediaCost
	case core.ReasoningBlock:
		tokens := estimateTextTokens(b.Text) + estimateTextTokens(b.Signature)
		if len(b.Redacted) > 0 {
			tokens += estimateEncodedReasoningTokens(int64(len(b.Redacted)))
		}
		if len(b.Extra) > 0 {
			tokens += estimateOpaqueJSONTokens(b.Extra)
		}
		return tokens
	case core.ToolUseBlock:
		return estimateTextTokens(b.ID) + estimateTextTokens(b.Name) + estimateJSONTokens(b.Arguments) + estimateOpaqueJSONTokens(b.Extra)
	case core.ToolResultBlock:
		tokens := estimateTextTokens(b.ToolUseID)
		for _, child := range b.Content {
			tokens += estimateCoreBlockTokens(child)
		}
		return tokens
	case core.ToolReferenceBlock:
		return estimateTextTokens(b.ToolName) + estimateOpaqueJSONTokens(b.Extra)
	default:
		return 0
	}
}

func estimateCoreToolTokens(tool core.Tool) int64 {
	return estimateTextTokens(tool.Name) + estimateTextTokens(tool.Description) + estimateJSONTokens([]byte(tool.Parameters))
}

func estimateImageBlockTokens(block core.ImageBlock) int64 {
	// A valid data URL is decoded by ToCoreRequest into Data. Keep this
	// fallback for malformed/URL-backed images so the URL itself is never
	// counted as ordinary text.
	decodedBytes := int64(len(block.Data))
	if decodedBytes == 0 {
		if _, payload, ok := strings.Cut(block.URL, ","); ok {
			decodedBytes = int64(len(payload)) * 3 / 4
		}
	}
	if decodedBytes <= 0 {
		return requestTokenMediaCost
	}
	tokens := decodedBytes / 256
	if tokens < requestTokenMinImage {
		tokens = requestTokenMinImage
	}
	if tokens > requestTokenMaxImage {
		tokens = requestTokenMaxImage
	}
	return tokens
}

// estimateTextTokens uses a small tokenizer-independent approximation. Latin
// text is close to four bytes per token; dense CJK/Han/Kana text is closer to
// one token per rune. It is only used before provider usage exists.
func estimateTextTokens(value string) int64 {
	if value == "" {
		return 0
	}
	var denseRunes, otherBytes int64
	for _, r := range value {
		if denseTokenRune(r) {
			denseRunes++
			continue
		}
		otherBytes += int64(utf8.RuneLen(r))
	}
	return denseRunes + ceilDiv(otherBytes, int64(domain.RequestTokenCharsPerToken))
}

func estimateJSONTokens(raw []byte) int64 {
	if len(raw) == 0 {
		return 0
	}
	return ceilDiv(int64(len(raw)), int64(domain.RequestTokenCharsPerToken))
}

// estimateOpaqueJSONTokens counts provider replay items without treating
// encrypted_content as a base64 text prompt. The encrypted reasoning
// adjustment follows the Codex history accounting port: encoded_len*3/4 -
// 650, clamped at zero. Encrypted function output uses the smaller 9/16
// visible-size heuristic used by that same protocol.
func estimateOpaqueJSONTokens(raw []byte) int64 {
	if len(raw) == 0 {
		return 0
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return estimateJSONTokens(raw)
	}
	bytes := int64(len(raw)) + opaqueJSONAdjustment(value, "")
	if bytes <= 0 {
		return 0
	}
	return ceilDiv(bytes, int64(domain.RequestTokenCharsPerToken))
}

func opaqueJSONAdjustment(value any, parentType string) int64 {
	switch node := value.(type) {
	case []any:
		var adjustment int64
		for _, child := range node {
			adjustment += opaqueJSONAdjustment(child, parentType)
		}
		return adjustment
	case map[string]any:
		itemType, _ := node["type"].(string)
		if itemType == "" {
			itemType = parentType
		}
		var adjustment int64
		for key, child := range node {
			if key == "encrypted_content" {
				encoded, ok := child.(string)
				if !ok {
					continue
				}
				visibleBytes := int64(len(encoded))
				switch itemType {
				case "reasoning", "compaction":
					visibleBytes = estimateEncodedReasoningBytes(int64(len(encoded)))
				case "encrypted_content":
					visibleBytes = estimateEncodedFunctionOutputBytes(int64(len(encoded)))
				}
				adjustment += visibleBytes - int64(len(encoded))
				continue
			}
			adjustment += opaqueJSONAdjustment(child, itemType)
		}
		return adjustment
	default:
		return 0
	}
}

func estimateEncodedReasoningTokens(encodedBytes int64) int64 {
	return ceilDiv(estimateEncodedReasoningBytes(encodedBytes), int64(domain.RequestTokenCharsPerToken))
}

func estimateEncodedReasoningBytes(encodedBytes int64) int64 {
	if encodedBytes <= 0 {
		return 0
	}
	scaled := encodedBytes * 3 / 4
	if scaled <= 650 {
		return 0
	}
	return scaled - 650
}

func estimateEncodedFunctionOutputBytes(encodedBytes int64) int64 {
	if encodedBytes <= 0 {
		return 0
	}
	return (encodedBytes*9 + 15) / 16
}

func applyRequestTokenSafetyBuffer(tokens int64) int64 {
	buffered := float64(tokens) * domain.RequestTokenSafetyBuffer
	if buffered >= float64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(math.Ceil(buffered))
}

func ceilDiv(value, divisor int64) int64 {
	if value <= 0 || divisor <= 0 {
		return 0
	}
	return (value + divisor - 1) / divisor
}

func denseTokenRune(r rune) bool {
	return (r >= 0x2E80 && r <= 0x2FFF) ||
		(r >= 0x3040 && r <= 0x30FF) ||
		(r >= 0x3400 && r <= 0x9FFF) ||
		(r >= 0xAC00 && r <= 0xD7AF) ||
		(r >= 0xF900 && r <= 0xFAFF)
}
