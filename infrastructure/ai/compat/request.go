package compat

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"nusashell/infrastructure/ai/core"
)

func (p *Provider) buildRequest(req *core.Request, stream bool) ([]byte, []core.Warning, error) {
	if err := p.validateProviderOptions(req.ProviderOptions); err != nil {
		return nil, nil, err
	}
	if n, ok := req.ProviderOptions["n"]; ok && !isSingleOutput(n) {
		return nil, nil, fmt.Errorf("%s: provider option %q must be 1; core.Response supports a single output", p.Name(), "n")
	}
	body := map[string]any{"model": req.Model}
	var warnings []core.Warning
	if p.spec.Request.Warnings != nil {
		warnings = append(warnings, p.spec.Request.Warnings(req)...)
	}
	messages := req.Messages
	if p.spec.Request.JSONSchemaToPrompt && req.ResponseFormat != nil && req.ResponseFormat.Type == core.ResponseFormatJSONSchema && req.ResponseFormat.JSONSchema != nil {
		messages = injectJSONSchema(messages, req.ResponseFormat.JSONSchema)
	}
	if p.spec.Request.Messages != nil {
		converted, err := p.spec.Request.Messages(messages)
		if err != nil {
			return nil, nil, err
		}
		body["messages"] = converted
	} else {
		converted, err := convertMessagesWithSpec(messages, p.spec)
		if err != nil {
			return nil, nil, err
		}
		body["messages"] = converted
	}
	if stream {
		body["stream"] = true
		if !p.spec.Stream.OmitStreamOptions {
			body["stream_options"] = map[string]any{"include_usage": true}
		}
	}
	if req.MaxTokens != nil {
		body[p.spec.maxTokensField()] = *req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	if req.TopK != nil {
		body["top_k"] = *req.TopK
	}
	if req.FrequencyPenalty != nil {
		body["frequency_penalty"] = *req.FrequencyPenalty
	}
	if req.PresencePenalty != nil {
		body["presence_penalty"] = *req.PresencePenalty
	}
	if !p.spec.Request.OmitStop && len(req.Stop) > 0 {
		stop := append([]string(nil), req.Stop...)
		if p.spec.Request.MaxStopSequences > 0 && len(stop) > p.spec.Request.MaxStopSequences {
			return nil, nil, fmt.Errorf("%s: stop supports at most %d sequence(s), got %d", p.Name(), p.spec.Request.MaxStopSequences, len(stop))
		}
		body["stop"] = stop
	}
	if len(req.Tools) > 0 {
		if p.spec.Request.Tools != nil {
			converted, err := p.spec.Request.Tools(req.Tools)
			if err != nil {
				return nil, nil, err
			}
			body["tools"] = converted
		} else {
			tools, toolWarnings, err := convertTools(req.Tools, p.spec.Features.StrictTools)
			if err != nil {
				return nil, nil, err
			}
			warnings = append(warnings, toolWarnings...)
			body["tools"] = tools
		}
	}
	if req.ToolChoice != nil {
		body["tool_choice"] = req.ToolChoice
	}
	if req.ResponseFormat != nil && (p.spec.Request.ResponseFormat != nil || !p.spec.Request.JSONSchemaToPrompt) {
		if p.spec.Request.ResponseFormat != nil {
			converted, err := p.spec.Request.ResponseFormat(req.ResponseFormat)
			if err != nil {
				return nil, nil, err
			}
			if converted != nil {
				body["response_format"] = converted
			}
		} else if converted, err := p.convertResponseFormat(req.ResponseFormat); err != nil {
			return nil, nil, err
		} else if converted != nil {
			body["response_format"] = converted
		}
	}
	if err := req.Thinking.Validate(); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", p.Name(), err)
	}
	if req.Thinking != nil && req.Thinking.Mode != core.ThinkingUnspecified {
		if p.spec.Request.Thinking == nil {
			return nil, nil, fmt.Errorf("%s: thinking is not supported", p.Name())
		}
		fields, err := p.spec.Request.Thinking(req.Thinking, req.Model)
		if err != nil {
			return nil, nil, err
		}
		for key, value := range fields {
			body[key] = value
		}
	}
	if p.spec.Request.ProviderOptions != nil {
		mappedOptions, passthroughOptions := p.splitProviderOptions(req.ProviderOptions)
		if err := p.spec.Request.ProviderOptions(mappedOptions, body, req); err != nil {
			return nil, nil, err
		}
		for key, value := range passthroughOptions {
			if err := p.putProviderOption(body, key, value); err != nil {
				return nil, nil, err
			}
		}
	} else {
		for key, value := range req.ProviderOptions {
			if err := p.putProviderOption(body, key, value); err != nil {
				return nil, nil, err
			}
		}
	}
	data, err := json.Marshal(body)
	return data, warnings, err
}

func isSingleOutput(value any) bool {
	switch value := value.(type) {
	case int:
		return value == 1
	case int64:
		return value == 1
	case float64:
		return value == 1
	}
	return false
}

func (p *Provider) putProviderOption(body map[string]any, key string, value any) error {
	if _, exists := body[key]; exists {
		return fmt.Errorf("%s: provider option %q conflicts with generated request field", p.Name(), key)
	}
	body[key] = value
	return nil
}

func (p *Provider) validateProviderOptions(options core.ProviderOptions) error {
	if len(options) == 0 || p.cfg.AllowUnknownProviderOptions || p.spec.Request.AllowUnknownProviderOptions {
		return nil
	}
	for key := range options {
		if _, ok := p.spec.Request.AllowedProviderOptions[key]; !ok {
			return fmt.Errorf("%s: unsupported provider option %q", p.Name(), key)
		}
	}
	return nil
}

func (p *Provider) splitProviderOptions(options core.ProviderOptions) (core.ProviderOptions, core.ProviderOptions) {
	if len(options) == 0 || !p.cfg.AllowUnknownProviderOptions || len(p.spec.Request.AllowedProviderOptions) == 0 {
		return options, nil
	}
	mapped := make(core.ProviderOptions)
	passthrough := make(core.ProviderOptions)
	for key, value := range options {
		if _, ok := p.spec.Request.AllowedProviderOptions[key]; ok {
			mapped[key] = value
		} else {
			passthrough[key] = value
		}
	}
	return mapped, passthrough
}

func convertMessages(messages []core.Message) ([]map[string]any, error) {
	return convertMessagesWithSpec(messages, Spec{})
}

func convertMessagesWithSpec(messages []core.Message, spec Spec) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(messages))
	for i, msg := range messages {
		switch msg.Role {
		case core.RoleSystem, core.RoleUser, core.RoleAssistant:
			content, toolCalls, reasoning, err := convertBlocks(msg.Blocks, spec)
			if err != nil {
				return nil, fmt.Errorf("messages[%d]: %w", i, err)
			}
			converted := map[string]any{"role": string(msg.Role)}
			if content != nil {
				converted["content"] = content
			} else if msg.Role == core.RoleAssistant && len(toolCalls) > 0 && spec.Request.EmitEmptyAssistantContentWithToolCalls {
				converted["content"] = ""
			}
			if len(toolCalls) > 0 {
				converted["tool_calls"] = toolCalls
			}
			for key, value := range reasoning {
				converted[key] = value
			}
			out = append(out, converted)
		case core.RoleTool:
			for _, block := range msg.Blocks {
				result, ok := block.(core.ToolResultBlock)
				if !ok {
					return nil, fmt.Errorf("messages[%d]: tool role only supports ToolResultBlock, got %T", i, block)
				}
				text, err := textOnly(result.Content)
				if err != nil {
					return nil, err
				}
				out = append(out, map[string]any{"role": "tool", "tool_call_id": result.ToolUseID, "content": text})
			}
		default:
			return nil, fmt.Errorf("messages[%d]: unsupported role %q", i, msg.Role)
		}
	}
	return out, nil
}

// compatAudioFormat normalizes an audio block's media type to the
// input_audio format name. Unknown or empty media types default to mp3.
func compatAudioFormat(block core.AudioBlock) string {
	switch strings.ToLower(strings.TrimSpace(block.MIME)) {
	case "audio/wav", "audio/x-wav", "audio/wave", "audio/vnd.wave":
		return "wav"
	case "audio/ogg", "audio/opus":
		return "ogg"
	case "audio/webm":
		return "webm"
	case "audio/flac", "audio/x-flac":
		return "flac"
	default:
		return "mp3"
	}
}

func convertBlocks(blocks []core.Block, spec Spec) (any, []map[string]any, map[string]any, error) {
	parts := make([]map[string]any, 0, len(blocks))
	var text strings.Builder
	var tools []map[string]any
	reasoning := make(map[string]any)
	for _, block := range blocks {
		switch b := block.(type) {
		case core.TextBlock:
			if b.Text != "" {
				parts = append(parts, map[string]any{"type": "text", "text": b.Text})
				if text.Len() > 0 {
					text.WriteString("\n")
				}
				text.WriteString(b.Text)
			}
		case core.ImageBlock:
			if b.URL == "" {
				return nil, nil, nil, fmt.Errorf("compat image blocks require URL")
			}
			image := map[string]any{"url": b.URL}
			if b.Detail != "" {
				image["detail"] = b.Detail
			}
			parts = append(parts, map[string]any{"type": "image_url", "image_url": image})
		case core.AudioBlock:
			parts = append(parts, map[string]any{
				"type": "input_audio",
				"input_audio": map[string]any{
					"data":   base64.StdEncoding.EncodeToString(b.Data),
					"format": compatAudioFormat(b),
				},
			})
		case core.VideoBlock:
			if b.URL == "" {
				return nil, nil, nil, fmt.Errorf("compat video blocks require URL")
			}
			parts = append(parts, map[string]any{"type": "video_url", "video_url": map[string]any{"url": b.URL}})
		case core.ToolUseBlock:
			tools = append(tools, map[string]any{
				"id":   b.ID,
				"type": "function",
				"function": map[string]any{
					"name":      b.Name,
					"arguments": string(b.Arguments),
				},
			})
		case core.ReasoningBlock:
			if err := putReasoningBlock(reasoning, b, spec); err != nil {
				return nil, nil, nil, err
			}
		default:
			return nil, nil, nil, fmt.Errorf("unsupported block %T", block)
		}
	}
	if len(parts) == 0 {
		return nil, tools, reasoning, nil
	}
	if len(parts) == 1 && text.Len() > 0 {
		return text.String(), tools, reasoning, nil
	}
	return parts, tools, reasoning, nil
}

func putReasoningBlock(out map[string]any, block core.ReasoningBlock, spec Spec) error {
	fields := spec.Response.ReasoningFields
	if len(fields) == 0 {
		return fmt.Errorf("ReasoningBlock history is not supported by this compat provider")
	}
	field := fields[0]
	if len(block.Extra) > 0 {
		var decoded any
		if err := json.Unmarshal(block.Extra, &decoded); err != nil {
			return fmt.Errorf("ReasoningBlock extra must be valid JSON: %w", err)
		}
		out[field] = decoded
		return nil
	}
	if block.Text != "" {
		out[field] = block.Text
		return nil
	}
	if block.Signature != "" || len(block.Redacted) > 0 {
		return fmt.Errorf("ReasoningBlock has provider state that this compat provider cannot encode")
	}
	return fmt.Errorf("ReasoningBlock has empty reasoning text with no extra or signature — reasoning was received from the provider but is missing on replay (input != output)")
}

func textOnly(blocks []core.Block) (string, error) {
	var out strings.Builder
	for _, block := range blocks {
		text, ok := block.(core.TextBlock)
		if !ok {
			return "", fmt.Errorf("compat tool result only supports text blocks, got %T", block)
		}
		if out.Len() > 0 {
			out.WriteString("\n")
		}
		out.WriteString(text.Text)
	}
	return out.String(), nil
}

func convertTools(tools []core.Tool, mode StrictToolMode) ([]map[string]any, []core.Warning, error) {
	var hasStrict, hasNonStrict bool
	for _, tool := range tools {
		switch tool.Strict {
		case core.StrictEnabled:
			hasStrict = true
		case core.StrictDisabled:
			hasNonStrict = true
		}
	}
	if mode == StrictToolsRequireAll && hasStrict && hasNonStrict {
		return nil, nil, fmt.Errorf("strict tool use requires all tools to be strict")
	}
	out := make([]map[string]any, 0, len(tools))
	var warnings []core.Warning
	for _, tool := range tools {
		var params any = map[string]any{"type": "object"}
		if len(tool.Parameters) > 0 {
			var decoded any
			if err := json.Unmarshal(tool.Parameters, &decoded); err != nil {
				return nil, nil, fmt.Errorf("tool %q parameters must be valid JSON: %w", tool.Name, err)
			}
			params = decoded
		}
		fn := map[string]any{"name": tool.Name, "description": tool.Description, "parameters": params}
		switch mode {
		case StrictToolsOmit:
			if tool.Strict != core.StrictDefault {
				warnings = append(warnings, core.Warning{
					Code:    "request.strict_tool_omitted",
					Message: fmt.Sprintf("tool %q strict setting is not supported by this provider and was omitted", tool.Name),
				})
			}
		case StrictToolsForward:
			if tool.Strict == core.StrictEnabled {
				fn["strict"] = true
			} else if tool.Strict == core.StrictDisabled {
				fn["strict"] = false
			}
		case StrictToolsRequireAll:
			if hasStrict {
				fn["strict"] = true
			}
		case StrictToolsAlways:
			if tool.Strict == core.StrictDisabled {
				return nil, nil, fmt.Errorf("tool %q cannot disable strict mode for this provider", tool.Name)
			}
		}
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out, warnings, nil
}

func (p *Provider) convertResponseFormat(format *core.ResponseFormat) (any, error) {
	switch format.Type {
	case core.ResponseFormatText:
		return nil, nil
	case core.ResponseFormatJSONObject:
		return map[string]any{"type": "json_object"}, nil
	case core.ResponseFormatJSONSchema:
		if !p.spec.Request.SupportsJSONSchema {
			return nil, fmt.Errorf("%s: json_schema response format is not supported", p.Name())
		}
		var schema any
		if p.spec.Request.CleanSchema != nil {
			var err error
			schema, err = p.spec.Request.CleanSchema(format.JSONSchema.Schema, format.JSONSchema.Strict)
			if err != nil {
				return nil, fmt.Errorf("%s: clean response schema: %w", p.Name(), err)
			}
		} else if err := json.Unmarshal(format.JSONSchema.Schema, &schema); err != nil {
			return nil, fmt.Errorf("%s: response schema must be valid JSON: %w", p.Name(), err)
		}
		return map[string]any{"type": "json_schema", "json_schema": map[string]any{
			"name":        format.JSONSchema.Name,
			"description": format.JSONSchema.Description,
			"schema":      schema,
			"strict":      format.JSONSchema.Strict == core.StrictEnabled,
		}}, nil
	default:
		return nil, fmt.Errorf("%s: unsupported response format %q", p.Name(), format.Type)
	}
}

func injectJSONSchema(messages []core.Message, schema *core.JSONSchema) []core.Message {
	out := make([]core.Message, len(messages))
	copy(out, messages)
	text := "\n\nReturn JSON matching schema " + schema.Name + ": " + string(schema.Schema)
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Role == core.RoleUser {
			out[i].Blocks = append(out[i].Blocks, core.Text(text))
			return out
		}
	}
	return append(out, core.UserText(strings.TrimSpace(text)))
}
