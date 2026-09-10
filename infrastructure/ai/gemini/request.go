package gemini

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"nusashell/infrastructure/ai/core"
)

const providerName = "gemini"

func warning(code, message string) core.Warning {
	return core.Warning{Code: code, Provider: providerName, Message: message}
}

// buildRequest converts a core request into the generateContent body.
//
// Ported from litellm's VertexGeminiConfig.transform_request +
// _transform_request_body (litellm/llms/vertex_ai/gemini/transformation.py and
// vertex_and_google_ai_studio_gemini.py). The stream flag is carried by the
// endpoint (streamGenerateContent), not by the body, so it is unused here.
func (p *Provider) buildRequest(req *core.Request, _ bool) (*generateContentRequest, []core.Warning, error) {
	if strings.TrimSpace(req.Model) == "" {
		return nil, nil, fmt.Errorf("gemini: model is required")
	}
	if err := req.Thinking.Validate(); err != nil {
		return nil, nil, fmt.Errorf("gemini: %w", err)
	}
	if err := validateProviderOptions(req.ProviderOptions); err != nil {
		return nil, nil, err
	}
	model := normalizeModelID(req.Model)
	warnings := make([]core.Warning, 0, 2)

	contents, system, err := convertMessages(model, req.Messages)
	if err != nil {
		return nil, nil, err
	}
	if len(contents) == 0 {
		// Gemini rejects an empty contents array; litellm inserts a single
		// blank user turn for the same reason.
		contents = []content{{Role: "user", Parts: []part{{Text: " "}}}}
	}
	out := &generateContentRequest{Contents: contents}
	if len(system) > 0 {
		out.SystemInstruction = &content{Parts: system}
	}

	cfg := &generationConfig{
		Temperature:     req.Temperature,
		TopP:            req.TopP,
		TopK:            req.TopK,
		MaxOutputTokens: req.MaxTokens,
		StopSequences:   append([]string(nil), req.Stop...),
	}
	if isGemini3(model) {
		// Gemini 3 rejects the sampling penalties that older models accept.
		if req.PresencePenalty != nil || req.FrequencyPenalty != nil {
			warnings = append(warnings, warning("gemini.penalty_unsupported", "Gemini 3 models do not accept presence_penalty or frequency_penalty; the parameters were dropped"))
		}
	} else {
		cfg.PresencePenalty = req.PresencePenalty
		cfg.FrequencyPenalty = req.FrequencyPenalty
	}

	thinking, thinkingWarnings, err := convertThinking(model, req.Thinking)
	if err != nil {
		return nil, nil, err
	}
	warnings = append(warnings, thinkingWarnings...)
	cfg.ThinkingConfig = thinking

	if err := applyResponseFormat(req.ResponseFormat, cfg); err != nil {
		return nil, nil, err
	}
	tools, toolConfig, err := convertTools(req.Tools, req.ToolChoice)
	if err != nil {
		return nil, nil, err
	}
	out.Tools = tools
	out.ToolConfig = toolConfig
	if !isEmptyGenerationConfig(cfg) {
		out.GenerationConfig = cfg
	}
	return out, warnings, nil
}

// validateProviderOptions rejects provider options this wire cannot express.
// Options that belong to another provider's transport (session affinity) are
// accepted as no-ops so a shared request builder cannot fail the call.
func validateProviderOptions(options core.ProviderOptions) error {
	for key := range options {
		switch key {
		case "session_id", "prompt_cache_key":
			// Gemini caching is implicit; there is no cache key on the wire.
		default:
			return fmt.Errorf("gemini: unsupported provider option %q", key)
		}
	}
	return nil
}

func isEmptyGenerationConfig(cfg *generationConfig) bool {
	if cfg == nil {
		return true
	}
	return cfg.Temperature == nil &&
		cfg.TopP == nil &&
		cfg.TopK == nil &&
		cfg.MaxOutputTokens == nil &&
		len(cfg.StopSequences) == 0 &&
		cfg.ResponseMimeType == "" &&
		len(cfg.ResponseSchema) == 0 &&
		cfg.PresencePenalty == nil &&
		cfg.FrequencyPenalty == nil &&
		cfg.CandidateCount == nil &&
		cfg.ThinkingConfig == nil &&
		len(cfg.ResponseModalities) == 0
}

// convertMessages maps the core message list onto Gemini contents plus an
// optional systemInstruction. Consecutive same-role messages are merged
// (Gemini requires alternating user/model turns) and tool results become
// functionResponse parts in a user turn, which must follow the model turn that
// requested them.
func convertMessages(model string, messages []core.Message) ([]content, []part, error) {
	builder := &messageBuilder{model: model, toolNames: make(map[string]string)}
	for _, msg := range messages {
		switch msg.Role {
		case core.RoleSystem:
			parts, err := textParts(msg.Blocks)
			if err != nil {
				return nil, nil, err
			}
			builder.system = append(builder.system, parts...)
		case core.RoleAssistant:
			parts, err := builder.assistantParts(msg.Blocks)
			if err != nil {
				return nil, nil, err
			}
			builder.add("model", parts)
		case core.RoleTool:
			parts, err := builder.toolResultParts(msg.Blocks)
			if err != nil {
				return nil, nil, err
			}
			builder.add("user", parts)
		default:
			parts, err := userParts(msg.Blocks)
			if err != nil {
				return nil, nil, err
			}
			builder.add("user", parts)
		}
	}
	return builder.contents, builder.system, nil
}

type messageBuilder struct {
	model    string
	system   []part
	contents []content
	// toolNames maps tool call IDs to function names so a later functionResponse
	// can name the function it answers (the core block only carries the ID).
	toolNames map[string]string
}

func (b *messageBuilder) add(role string, parts []part) {
	if len(parts) == 0 {
		return
	}
	if n := len(b.contents); n > 0 && b.contents[n-1].Role == role {
		b.contents[n-1].Parts = append(b.contents[n-1].Parts, parts...)
		return
	}
	b.contents = append(b.contents, content{Role: role, Parts: parts})
}

func textParts(blocks []core.Block) ([]part, error) {
	parts := make([]part, 0, len(blocks))
	for _, block := range blocks {
		text, ok := block.(core.TextBlock)
		if !ok {
			return nil, fmt.Errorf("gemini: unsupported %T in a system message", block)
		}
		if text.Text == "" {
			continue
		}
		parts = append(parts, part{Text: text.Text})
	}
	return parts, nil
}

func userParts(blocks []core.Block) ([]part, error) {
	parts := make([]part, 0, len(blocks))
	for _, block := range blocks {
		switch block.(type) {
		case core.TextBlock, core.ImageBlock, core.AudioBlock, core.VideoBlock:
			if text, ok := block.(core.TextBlock); ok {
				if text.Text == "" {
					continue
				}
				parts = append(parts, part{Text: text.Text})
				continue
			}
			media, err := mediaPart(block)
			if err != nil {
				return nil, err
			}
			if media != nil {
				parts = append(parts, *media)
			}
		case core.ToolReferenceBlock:
			// Gemini has no tool-reference part; the declaration already
			// travels in the request tools.
		default:
			return nil, fmt.Errorf("gemini: unsupported %T in a user message", block)
		}
	}
	return parts, nil
}

// assistantParts converts a model turn. Thought parts replay as thought parts,
// tool calls replay as functionCall parts (with their thought signature, which
// Gemini 3 requires on the first call of a batch), and a signature that arrived
// on a text part is re-attached to that text part.
func (b *messageBuilder) assistantParts(blocks []core.Block) ([]part, error) {
	parts := make([]part, 0, len(blocks))
	pendingTextSignature := ""
	for _, block := range blocks {
		switch v := block.(type) {
		case core.ReasoningBlock:
			if len(v.Redacted) > 0 {
				// Gemini has no redacted-reasoning part.
				continue
			}
			signature := firstNonEmpty(v.Signature, signatureFromExtra(v.Extra))
			if v.Text == "" {
				if signature != "" {
					pendingTextSignature = signature
				}
				continue
			}
			parts = append(parts, part{Text: v.Text, Thought: true, ThoughtSignature: signature})
		case core.TextBlock:
			text := part{Text: v.Text, ThoughtSignature: pendingTextSignature}
			pendingTextSignature = ""
			if text.Text == "" {
				continue
			}
			parts = append(parts, text)
		case core.ToolUseBlock:
			call := &functionCall{Name: v.Name}
			if len(v.Arguments) == 0 || !json.Valid(v.Arguments) {
				call.Args = json.RawMessage("{}")
			} else {
				call.Args = v.Arguments
			}
			// Gemini 3+ accepts and returns functionCall.id for strict
			// tool-call matching; older models reject the field.
			if isGemini3(b.model) {
				call.ID = v.ID
			}
			callPart := part{FunctionCall: call}
			callPart.ThoughtSignature = firstNonEmpty(v.Signature, signatureFromExtra(v.Extra))
			parts = append(parts, callPart)
			if v.ID != "" && v.Name != "" {
				b.toolNames[v.ID] = v.Name
			}
		case core.ImageBlock, core.AudioBlock, core.VideoBlock:
			media, err := mediaPart(block)
			if err != nil {
				return nil, err
			}
			if media != nil {
				parts = append(parts, *media)
			}
		case core.ToolResultBlock:
			return nil, fmt.Errorf("gemini: tool results must be sent in a tool message, not in an assistant message")
		case core.ToolReferenceBlock:
		default:
			return nil, fmt.Errorf("gemini: unsupported %T in an assistant message", block)
		}
	}
	applyDummyThoughtSignature(b.model, parts)
	return parts, nil
}

// applyDummyThoughtSignature backfills the first functionCall part with
// litellm's sentinel signature when the model requires one but the history
// (for example a conversation migrated from another provider) has none.
func applyDummyThoughtSignature(model string, parts []part) {
	if !isGemini3(model) {
		return
	}
	for i := range parts {
		if parts[i].FunctionCall == nil {
			continue
		}
		if parts[i].ThoughtSignature == "" {
			parts[i].ThoughtSignature = dummyThoughtSignature
		}
		return
	}
}

func (b *messageBuilder) toolResultParts(blocks []core.Block) ([]part, error) {
	parts := make([]part, 0, len(blocks))
	for _, block := range blocks {
		result, ok := block.(core.ToolResultBlock)
		if !ok {
			return nil, fmt.Errorf("gemini: unsupported %T in a tool message", block)
		}
		name := b.toolNames[result.ToolUseID]
		if name == "" {
			return nil, fmt.Errorf("gemini: tool result %q has no matching tool call in this request", result.ToolUseID)
		}
		text, media, err := splitToolResult(result.Content)
		if err != nil {
			return nil, err
		}
		payload, err := toolResponsePayload(text)
		if err != nil {
			return nil, err
		}
		response := &functionResponse{Name: name, Response: payload}
		if isGemini3(b.model) && result.ToolUseID != "" {
			response.ID = result.ToolUseID
		}
		if len(media) > 0 {
			// Multimodal results must nest inside functionResponse.parts.
			response.Parts = media
		}
		parts = append(parts, part{FunctionResponse: response})
	}
	return parts, nil
}

func splitToolResult(blocks []core.Block) (string, []part, error) {
	var text strings.Builder
	var media []part
	for _, block := range blocks {
		switch v := block.(type) {
		case core.TextBlock:
			text.WriteString(v.Text)
		case core.ImageBlock, core.AudioBlock, core.VideoBlock:
			part, err := mediaPart(block)
			if err != nil {
				return "", nil, err
			}
			if part != nil {
				media = append(media, *part)
			}
		}
	}
	return text.String(), media, nil
}

// toolResponsePayload mirrors litellm: a tool result that is already a JSON
// object becomes the functionResponse payload verbatim so structured results
// stay structured; anything else is wrapped in {"content": ...}.
func toolResponsePayload(text string) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") {
		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
			if _, ok := decoded.(map[string]any); ok {
				return json.RawMessage(trimmed), nil
			}
		}
	}
	payload, err := json.Marshal(map[string]string{"content": text})
	if err != nil {
		return nil, fmt.Errorf("gemini: encode tool result: %w", err)
	}
	return payload, nil
}

// mediaPart converts an image, audio, or video block into an inlineData part
// (bytes we already hold) or a fileData part (Files API / public / gs:// URIs).
func mediaPart(block core.Block) (*part, error) {
	switch v := block.(type) {
	case core.ImageBlock:
		return mediaFromInput(v.URL, v.FileURI, v.Data, v.MIME)
	case core.AudioBlock:
		if len(v.Data) == 0 {
			return nil, fmt.Errorf("gemini: audio input requires inline data")
		}
		mime := firstNonEmpty(v.MIME, audioMIME(v.Format))
		if mime == "" {
			return nil, fmt.Errorf("gemini: audio input requires a MIME type")
		}
		return &part{InlineData: &blob{
			MimeType: mime,
			Data:     base64.StdEncoding.EncodeToString(v.Data),
		}}, nil
	case core.VideoBlock:
		return mediaFromInput(v.URL, "", nil, v.MIME)
	default:
		return nil, fmt.Errorf("gemini: unsupported media block %T", block)
	}
}

func mediaFromInput(url, fileURI string, data []byte, mime string) (*part, error) {
	if len(data) > 0 {
		resolved := firstNonEmpty(mime, mimeFromDataURL(url))
		if resolved == "" {
			return nil, fmt.Errorf("gemini: inline media requires a MIME type")
		}
		return &part{InlineData: &blob{
			MimeType: resolved,
			Data:     base64.StdEncoding.EncodeToString(data),
		}}, nil
	}
	raw := firstNonEmpty(fileURI, url)
	if raw == "" {
		return nil, fmt.Errorf("gemini: media requires a URI or inline data")
	}
	resolvedMime := firstNonEmpty(mime, mimeFromDataURL(raw))
	if payload, ok := dataURLPayload(raw); ok {
		if resolvedMime == "" {
			return nil, fmt.Errorf("gemini: inline media requires a MIME type")
		}
		return &part{InlineData: &blob{MimeType: resolvedMime, Data: payload}}, nil
	}
	if !isSupportedURI(raw) {
		return nil, fmt.Errorf("gemini: unsupported media URI %q", raw)
	}
	return &part{FileData: &fileData{
		MimeType: firstNonEmpty(resolvedMime, mimeFromURL(raw)),
		FileURI:  raw,
	}}, nil
}

func isSupportedURI(raw string) bool {
	return strings.HasPrefix(raw, "https://") ||
		strings.HasPrefix(raw, "http://") ||
		strings.HasPrefix(raw, "gs://") ||
		strings.HasPrefix(raw, filesAPIPrefix)
}

// dataURLPayload returns the base64 payload of a data URL without decoding it
// (the wire expects base64 anyway).
func dataURLPayload(raw string) (string, bool) {
	if !strings.HasPrefix(raw, "data:") {
		return "", false
	}
	header, payload, ok := strings.Cut(raw, ",")
	if !ok || !strings.Contains(header, ";base64") || payload == "" {
		return "", false
	}
	return payload, true
}

func mimeFromDataURL(raw string) string {
	if !strings.HasPrefix(raw, "data:") {
		return ""
	}
	header, _, ok := strings.Cut(raw, ",")
	if !ok {
		return ""
	}
	mime := strings.TrimPrefix(header, "data:")
	if idx := strings.IndexByte(mime, ';'); idx >= 0 {
		mime = mime[:idx]
	}
	return strings.TrimSpace(mime)
}

var mimeByExtension = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".gif":  "image/gif",
	".heic": "image/heic",
	".heif": "image/heif",
	".bmp":  "image/bmp",
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".mov":  "video/quicktime",
	".mpeg": "video/mpeg",
	".mp3":  "audio/mp3",
	".wav":  "audio/wav",
	".ogg":  "audio/ogg",
	".flac": "audio/flac",
	".m4a":  "audio/m4a",
	".aac":  "audio/aac",
	".pdf":  "application/pdf",
	".txt":  "text/plain",
}

func mimeFromURL(raw string) string {
	trimmed := raw
	if idx := strings.IndexAny(trimmed, "?#"); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	return mimeByExtension[strings.ToLower(path.Ext(trimmed))]
}

func audioMIME(format string) string {
	value := strings.ToLower(strings.TrimSpace(format))
	if value == "" {
		return ""
	}
	if strings.Contains(value, "/") {
		return value
	}
	return firstNonEmpty(mimeByExtension["."+value], "audio/"+value)
}

// convertThinking maps the portable thinking request onto thinkingConfig.
// Gemini 2.x and older take a token budget; Gemini 3 takes a level. Ported from
// litellm's _map_reasoning_effort_to_thinking_budget /
// _map_reasoning_effort_to_thinking_level.
func convertThinking(model string, thinking *core.Thinking) (*thinkingConfig, []core.Warning, error) {
	if thinking == nil || thinking.Mode == core.ThinkingUnspecified {
		return nil, nil, nil
	}
	effort := strings.ToLower(strings.TrimSpace(thinking.Effort))
	if thinking.Mode == core.ThinkingDisabled || effort == "none" || effort == "disable" {
		cfg := &thinkingConfig{IncludeThoughts: core.Bool(false)}
		if isGemini3(model) {
			// Gemini 3 cannot fully disable thinking; the lowest advertised
			// level for this model is the documented floor.
			cfg.ThinkingLevel = gemini3ThinkingLevels(model)[0]
		} else {
			cfg.ThinkingBudget = core.IntPtr(0)
		}
		return cfg, nil, nil
	}

	cfg := &thinkingConfig{IncludeThoughts: core.Bool(true)}
	if effort == "" || effort == "auto" {
		// Keep the provider's dynamic-thinking default while still asking for
		// thought summaries.
		return cfg, nil, nil
	}
	if isGemini3(model) {
		normalized, err := normalizeThinkingEffort(effort)
		if err != nil {
			return nil, nil, err
		}
		supported := gemini3ThinkingLevels(model)
		level, substituted := clampThinkingLevel(normalized, supported)
		cfg.ThinkingLevel = level
		var warnings []core.Warning
		if substituted {
			warnings = append(warnings, warning("gemini.thinking_level_clamped", fmt.Sprintf(
				"gemini: model %q does not support thinking level %q; clamped to %q",
				model, effort, level)))
		}
		if thinking.BudgetTokens != nil {
			warnings = append(warnings, warning("gemini.thinking_budget_unsupported", "Gemini 3 models use thinking levels instead of thinking budgets; the budget was replaced by the matching level"))
		}
		return cfg, warnings, nil
	}
	if thinking.BudgetTokens != nil {
		if *thinking.BudgetTokens < 0 {
			return nil, nil, fmt.Errorf("gemini: thinking budget must not be negative")
		}
		cfg.ThinkingBudget = core.IntPtr(*thinking.BudgetTokens)
		return cfg, nil, nil
	}
	budget, err := thinkingBudgetFor(model, effort)
	if err != nil {
		return nil, nil, err
	}
	cfg.ThinkingBudget = core.IntPtr(budget)
	return cfg, nil, nil
}

// thinkingBudgetFor ports litellm's budget table for an explicit thinking
// effort. Empty/auto effort never reaches here: convertThinking keeps the
// provider default (with thought summaries) via its early return before this
// function is called.
func thinkingBudgetFor(model string, effort string) (int, error) {
	lower := strings.ToLower(model)
	switch effort {
	case "minimal":
		switch {
		case strings.Contains(lower, "gemini-2.5-flash-lite"):
			return 512, nil
		case strings.Contains(lower, "gemini-2.5-pro"):
			return 128, nil
		case strings.Contains(lower, "gemini-2.5-flash"):
			return 1, nil
		default:
			return 128, nil
		}
	case "low":
		return 1024, nil
	case "medium":
		return 2048, nil
	case "high", "xhigh", "max":
		return 4096, nil
	default:
		return 0, fmt.Errorf("gemini: unknown thinking effort %q", effort)
	}
}

// gemini3ThinkingLevelEntry maps a model prefix to the thinking levels it
// supports, ordered from lowest to highest. More specific prefixes come first
// so a flash-lite-image entry is matched before the flash-lite and flash
// entries it contains. Source: ai.google.dev/gemini-api/docs/thinking.
type gemini3ThinkingLevelEntry struct {
	prefix string
	levels []string
}

// defaultGemini3ThinkingLevels is the conservative fallback for unknown or
// newly released Gemini 3 models: minimal is not assumed valid because most
// Gemini 3 tiers reject it.
var defaultGemini3ThinkingLevels = []string{"low", "medium", "high"}

var gemini3ThinkingLevelTable = []gemini3ThinkingLevelEntry{
	// Image variant: only minimal and high (no low/medium).
	{"gemini-3.1-flash-lite-image", []string{"minimal", "high"}},
	// Flash-Lite variants (3.5 and 3.1): minimal, low, medium, high.
	{"gemini-3.5-flash-lite", []string{"minimal", "low", "medium", "high"}},
	{"gemini-3.1-flash-lite", []string{"minimal", "low", "medium", "high"}},
	// 3.8 and 3.7 flash: low, medium, high (no minimal).
	{"gemini-3.8-flash", []string{"low", "medium", "high"}},
	{"gemini-3.7-flash", []string{"low", "medium", "high"}},
	// 3.6 and 3.5 flash: minimal, low, medium, high.
	{"gemini-3.6-flash", []string{"minimal", "low", "medium", "high"}},
	{"gemini-3.5-flash", []string{"minimal", "low", "medium", "high"}},
	// 3.1 pro: low, medium, high.
	{"gemini-3.1-pro", []string{"low", "medium", "high"}},
	// 3 pro: low, high only.
	{"gemini-3-pro", []string{"low", "high"}},
	// 3 flash preview (Gemini 3 Flash): minimal, low, medium, high.
	{"gemini-3-flash", []string{"minimal", "low", "medium", "high"}},
}

// gemini3ThinkingLevels returns the thinking levels a Gemini 3 model supports,
// ordered from lowest to highest, or nil when the model is not a Gemini 3
// model. Unknown Gemini 3 models fall back to the conservative
// {low, medium, high} set so minimal is never assumed valid.
func gemini3ThinkingLevels(model string) []string {
	lower := strings.ToLower(model)
	if !strings.Contains(lower, "gemini-3") {
		return nil
	}
	for _, entry := range gemini3ThinkingLevelTable {
		if strings.HasPrefix(lower, entry.prefix) {
			return entry.levels
		}
	}
	return defaultGemini3ThinkingLevels
}

// thinkingLevelRank orders the thinking levels from lowest to highest for
// nearest-level clamping.
var thinkingLevelRank = map[string]int{
	"minimal": 0, "low": 1, "medium": 2, "high": 3,
}

// clampThinkingLevel returns the nearest supported level for a requested
// level. When the requested level is unsupported and sits between two
// supported levels, the higher one wins (more reasoning is the safer
// default). The second return is true when a substitution was made.
func clampThinkingLevel(requested string, supported []string) (string, bool) {
	for _, s := range supported {
		if s == requested {
			return requested, false
		}
	}
	reqRank, ok := thinkingLevelRank[requested]
	if !ok {
		return supported[0], true
	}
	best := supported[0]
	bestDist := absInt(reqRank - thinkingLevelRank[best])
	for _, s := range supported[1:] {
		dist := absInt(reqRank - thinkingLevelRank[s])
		if dist < bestDist || (dist == bestDist && thinkingLevelRank[s] > thinkingLevelRank[best]) {
			best = s
			bestDist = dist
		}
	}
	return best, true
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// normalizeThinkingEffort maps the portable effort name onto a Gemini 3
// thinking level name. "xhigh" and "max" collapse to "high".
func normalizeThinkingEffort(effort string) (string, error) {
	switch effort {
	case "minimal", "low", "medium", "high":
		return effort, nil
	case "xhigh", "max":
		return "high", nil
	default:
		return "", fmt.Errorf("gemini: unknown thinking effort %q", effort)
	}
}

func convertTools(tools []core.Tool, choice core.ToolChoice) ([]tool, *toolConfig, error) {
	toolConfigValue, err := convertToolChoice(choice)
	if err != nil {
		return nil, nil, err
	}
	if len(tools) == 0 {
		if toolConfigValue != nil {
			return nil, nil, fmt.Errorf("gemini: tool_choice requires at least one tool")
		}
		return nil, nil, nil
	}
	declarations := make([]functionDeclaration, 0, len(tools))
	for _, t := range tools {
		if strings.TrimSpace(t.Name) == "" {
			return nil, nil, fmt.Errorf("gemini: tool name is required")
		}
		declaration := functionDeclaration{Name: t.Name, Description: t.Description}
		if len(t.Parameters) > 0 {
			schema, err := sanitizeSchema(json.RawMessage(t.Parameters), false)
			if err != nil {
				return nil, nil, fmt.Errorf("gemini: tool %q parameters: %w", t.Name, err)
			}
			declaration.Parameters = schema
		}
		declarations = append(declarations, declaration)
	}
	return []tool{{FunctionDeclarations: declarations}}, toolConfigValue, nil
}

func convertToolChoice(choice core.ToolChoice) (*toolConfig, error) {
	if choice == nil {
		return nil, nil
	}
	if value, ok := choice.(string); ok {
		return functionCallingConfigFor(value, "")
	}
	data, err := json.Marshal(choice)
	if err != nil {
		return nil, fmt.Errorf("gemini: tool_choice must be an object: %w", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil || decoded == nil {
		return nil, fmt.Errorf("gemini: tool_choice must be an object")
	}
	typ, _ := decoded["type"].(string)
	name, _ := decoded["name"].(string)
	if function, ok := decoded["function"].(map[string]any); ok && name == "" {
		name, _ = function["name"].(string)
	}
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "auto":
		return functionCallingConfigFor("auto", "")
	case "any", "required":
		return functionCallingConfigFor("required", "")
	case "none":
		return functionCallingConfigFor("none", "")
	case "tool", "function":
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("gemini: named tool_choice requires a tool name")
		}
		return functionCallingConfigFor("required", name)
	default:
		return nil, fmt.Errorf("gemini: unsupported tool_choice type %q", typ)
	}
}

func functionCallingConfigFor(value, name string) (*toolConfig, error) {
	mode := ""
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		mode = "AUTO"
	case "required", "any":
		mode = "ANY"
	case "none":
		mode = "NONE"
	default:
		return nil, fmt.Errorf("gemini: unsupported tool_choice %q", value)
	}
	config := &functionCallingConfig{Mode: mode}
	if mode == "ANY" && strings.TrimSpace(name) != "" {
		config.AllowedFunctionNames = []string{name}
	}
	return &toolConfig{FunctionCallingConfig: config}, nil
}

// applyResponseFormat maps structured output onto responseMimeType plus the
// OpenAPI-subset responseSchema Gemini accepts (litellm's default for models
// predating responseJsonSchema).
func applyResponseFormat(format *core.ResponseFormat, cfg *generationConfig) error {
	if format == nil {
		return nil
	}
	switch format.Type {
	case "", core.ResponseFormatText:
		return nil
	case core.ResponseFormatJSONObject:
		cfg.ResponseMimeType = "application/json"
		return nil
	case core.ResponseFormatJSONSchema:
		if format.JSONSchema == nil || len(format.JSONSchema.Schema) == 0 {
			return fmt.Errorf("gemini: response_format json_schema requires a schema")
		}
		schema, err := sanitizeSchema(json.RawMessage(format.JSONSchema.Schema), true)
		if err != nil {
			return fmt.Errorf("gemini: response_format schema: %w", err)
		}
		cfg.ResponseMimeType = "application/json"
		cfg.ResponseSchema = schema
		return nil
	default:
		return fmt.Errorf("gemini: unsupported response format %q", format.Type)
	}
}

func isGemini3(model string) bool {
	return strings.Contains(strings.ToLower(model), "gemini-3")
}
