package gemini

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"nusashell/infrastructure/ai/core"
	"nusashell/infrastructure/ai/internal/testgolden"
)

func mustTool(t *testing.T, name, description string, schema any) core.Tool {
	t.Helper()
	tool, err := core.NewTool(name, description, schema)
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	return tool
}

func TestBuildRequestBasicsAndMessageMerging(t *testing.T) {
	provider, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := &core.Request{
		Model: "gemini-2.5-flash",
		Messages: []core.Message{
			core.System("You are helpful."),
			core.UserText("Hello"),
			core.UserText("Again"),
			core.AssistantText("Hi there"),
		},
	}
	wire, warnings, err := provider.buildRequest(req, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", warnings)
	}
	if wire.SystemInstruction == nil || len(wire.SystemInstruction.Parts) != 1 || wire.SystemInstruction.Parts[0].Text != "You are helpful." {
		t.Fatalf("systemInstruction = %+v", wire.SystemInstruction)
	}
	if len(wire.Contents) != 2 {
		t.Fatalf("contents = %+v, want 2 (user/model)", wire.Contents)
	}
	user := wire.Contents[0]
	model := wire.Contents[1]
	if user.Role != "user" || len(user.Parts) != 2 || user.Parts[0].Text != "Hello" || user.Parts[1].Text != "Again" {
		t.Fatalf("user content = %+v, want consecutive user messages merged", user)
	}
	if model.Role != "model" || len(model.Parts) != 1 || model.Parts[0].Text != "Hi there" {
		t.Fatalf("model content = %+v", model)
	}
	if wire.GenerationConfig != nil {
		t.Fatalf("generationConfig = %+v, want none", wire.GenerationConfig)
	}
	testgolden.AssertJSON(t, "../../testdata/gemini/request_basics.golden.json", wire)
}

func TestBuildRequestToolRoundTripGemini25(t *testing.T) {
	provider, _ := New(Config{APIKey: "test"})
	req := &core.Request{
		Model: "gemini-2.5-flash",
		Messages: []core.Message{
			core.UserText("Use the tool"),
			core.Assistant(
				core.ReasoningBlock{Text: "I will look it up.", Signature: "sig-thought"},
				core.ToolUseBlock{ID: "call_1", Name: "lookup", Arguments: core.MustJSONRaw(map[string]any{"q": "x"})},
			),
			core.ToolResult("call_1", core.Text(`{"result": "ok"}`)),
		},
	}
	wire, _, err := provider.buildRequest(req, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if len(wire.Contents) != 3 {
		t.Fatalf("contents = %d, want 3", len(wire.Contents))
	}
	modelContent := wire.Contents[1]
	if modelContent.Role != "model" {
		t.Fatalf("contents[1].role = %q", modelContent.Role)
	}
	thought := modelContent.Parts[0]
	if !thought.Thought || thought.Text != "I will look it up." || thought.ThoughtSignature != "sig-thought" {
		t.Fatalf("thought part = %+v", thought)
	}
	call := modelContent.Parts[1]
	if call.FunctionCall == nil || call.FunctionCall.Name != "lookup" || call.FunctionCall.ID != "" {
		t.Fatalf("functionCall = %+v, want no id on Gemini 2.x", call.FunctionCall)
	}
	var args map[string]any
	if err := json.Unmarshal(call.FunctionCall.Args, &args); err != nil || args["q"] != "x" {
		t.Fatalf("functionCall args = %s (%v)", call.FunctionCall.Args, err)
	}
	toolUser := wire.Contents[2]
	response := toolUser.Parts[0].FunctionResponse
	if toolUser.Role != "user" || response == nil || response.Name != "lookup" || response.ID != "" {
		t.Fatalf("functionResponse = %+v", toolUser)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Response, &payload); err != nil || payload["result"] != "ok" {
		t.Fatalf("functionResponse.response = %s (%v)", response.Response, err)
	}
	testgolden.AssertJSON(t, "../../testdata/gemini/request_tools.golden.json", wire)
}

func TestBuildRequestToolRoundTripGemini3(t *testing.T) {
	provider, _ := New(Config{APIKey: "test"})
	req := &core.Request{
		Model: "gemini-3-pro-preview",
		Messages: []core.Message{
			core.UserText("Use the tool"),
			core.Assistant(core.ToolUseBlock{ID: "call_abc", Name: "lookup", Arguments: core.MustJSONRaw(map[string]any{"q": "x"})}),
			core.ToolResult("call_abc", core.Text("done")),
			core.Assistant(core.Text("finished")),
		},
	}
	wire, _, err := provider.buildRequest(req, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	call := wire.Contents[1].Parts[0]
	if call.FunctionCall == nil {
		t.Fatalf("functionCall = %+v", wire.Contents[1])
	}
	if call.FunctionCall.ID != "call_abc" {
		t.Fatalf("functionCall.id = %q, want call_abc (Gemini 3 forwards ids)", call.FunctionCall.ID)
	}
	if call.ThoughtSignature != dummyThoughtSignature {
		t.Fatalf("thoughtSignature = %q, want the documented placeholder when history has none", call.ThoughtSignature)
	}
	response := wire.Contents[2].Parts[0].FunctionResponse
	if response == nil || response.ID != "call_abc" || response.Name != "lookup" {
		t.Fatalf("functionResponse = %+v, want id + name echoed", response)
	}

	// A signature carried by the replay must be preserved, not replaced by the
	// placeholder, and applies only to the first functionCall part.
	req2 := &core.Request{
		Model: "gemini-3-pro-preview",
		Messages: []core.Message{
			core.UserText("parallel"),
			core.Assistant(
				core.ToolUseBlock{ID: "a", Name: "one", Arguments: core.MustJSONRaw(map[string]any{}), Signature: "sig-a"},
				core.ToolUseBlock{ID: "b", Name: "two", Arguments: core.MustJSONRaw(map[string]any{})},
			),
		},
	}
	wire2, _, err := provider.buildRequest(req2, false)
	if err != nil {
		t.Fatalf("buildRequest(parallel): %v", err)
	}
	parts := wire2.Contents[1].Parts
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(parts))
	}
	if parts[0].ThoughtSignature != "sig-a" {
		t.Fatalf("first call signature = %q, want sig-a", parts[0].ThoughtSignature)
	}
	if parts[1].ThoughtSignature != "" {
		t.Fatalf("second call signature = %q, want none (only the first part is signed)", parts[1].ThoughtSignature)
	}
}

func TestBuildRequestMultimodalToolResult(t *testing.T) {
	provider, _ := New(Config{APIKey: "test"})
	req := &core.Request{
		Model: "gemini-2.5-flash",
		Messages: []core.Message{
			core.Assistant(core.ToolUseBlock{ID: "c", Name: "read_media"}),
			core.Message{Role: core.RoleTool, Blocks: []core.Block{
				core.ToolResultBlock{ToolUseID: "c", Content: []core.Block{
					core.TextBlock{Text: "I found:"},
					core.ImageBlock{Data: []byte("PNG"), MIME: "image/png"},
				}},
			}},
		},
	}
	wire, _, err := provider.buildRequest(req, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if len(wire.Contents) != 2 {
		t.Fatalf("contents = %d, want 2", len(wire.Contents))
	}
	if wire.Contents[0].Role != "model" || wire.Contents[0].Parts[0].FunctionCall == nil {
		t.Fatalf("contents[0] = %+v, want the assistant tool call", wire.Contents[0])
	}
	response := wire.Contents[1].Parts[0].FunctionResponse
	if wire.Contents[1].Role != "user" || response == nil || response.Name != "read_media" {
		t.Fatalf("functionResponse = %+v", wire.Contents[1])
	}
	if len(response.Parts) != 1 || response.Parts[0].InlineData == nil || response.Parts[0].InlineData.MimeType != "image/png" {
		t.Fatalf("multimodal parts = %+v, want nested inlineData", response.Parts)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Response, &payload); err != nil || payload["content"] != "I found:" {
		t.Fatalf("response payload = %s (%v)", response.Response, err)
	}
}

func TestBuildRequestTextPartSignatureReplay(t *testing.T) {
	provider, _ := New(Config{APIKey: "test"})
	extra := core.MustJSONRaw(map[string]string{signatureExtraKey: "sig-text"})
	req := &core.Request{
		Model: "gemini-3-pro-preview",
		Messages: []core.Message{
			core.Assistant(
				core.ReasoningBlock{Text: "", Extra: extra},
				core.TextBlock{Text: "answer text"},
			),
		},
	}
	wire, _, err := provider.buildRequest(req, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	parts := wire.Contents[0].Parts
	if len(parts) != 1 {
		t.Fatalf("parts = %+v, want the signature folded into the text part", parts)
	}
	if parts[0].Text != "answer text" || parts[0].ThoughtSignature != "sig-text" {
		t.Fatalf("text part = %+v, want signature attached", parts[0])
	}
}

func TestBuildRequestThinking(t *testing.T) {
	cases := []struct {
		name            string
		model           string
		thinking        *core.Thinking
		want            *thinkingConfig
		wantErr         string
		wantWarningCode string
	}{
		{
			name:     "budget medium flash",
			model:    "gemini-2.5-flash",
			thinking: &core.Thinking{Mode: core.ThinkingEnabled, Effort: "medium"},
			want:     &thinkingConfig{ThinkingBudget: core.IntPtr(2048), IncludeThoughts: core.Bool(true)},
		},
		{
			name:     "minimal pro budget",
			model:    "gemini-2.5-pro",
			thinking: &core.Thinking{Mode: core.ThinkingEnabled, Effort: "minimal"},
			want:     &thinkingConfig{ThinkingBudget: core.IntPtr(128), IncludeThoughts: core.Bool(true)},
		},
		{
			name:     "high xhigh max all map to 4096",
			model:    "gemini-2.5-flash",
			thinking: &core.Thinking{Mode: core.ThinkingEnabled, Effort: "xhigh"},
			want:     &thinkingConfig{ThinkingBudget: core.IntPtr(4096), IncludeThoughts: core.Bool(true)},
		},
		{
			name:     "disabled zero budget",
			model:    "gemini-2.5-flash",
			thinking: &core.Thinking{Mode: core.ThinkingDisabled},
			want:     &thinkingConfig{ThinkingBudget: core.IntPtr(0), IncludeThoughts: core.Bool(false)},
		},
		{
			name:     "explicit budget",
			model:    "gemini-2.5-flash",
			thinking: &core.Thinking{Mode: core.ThinkingEnabled, Effort: "high", BudgetTokens: core.IntPtr(777)},
			want:     &thinkingConfig{ThinkingBudget: core.IntPtr(777), IncludeThoughts: core.Bool(true)},
		},
		{
			name:     "gemini3 high level",
			model:    "gemini-3-flash",
			thinking: &core.Thinking{Mode: core.ThinkingEnabled, Effort: "high"},
			want:     &thinkingConfig{ThinkingLevel: "high", IncludeThoughts: core.Bool(true)},
		},
		{
			name:            "gemini3 medium on pro maps to high with clamp warning",
			model:           "gemini-3-pro-preview",
			thinking:        &core.Thinking{Mode: core.ThinkingEnabled, Effort: "medium"},
			want:            &thinkingConfig{ThinkingLevel: "high", IncludeThoughts: core.Bool(true)},
			wantWarningCode: "gemini.thinking_level_clamped",
		},
		{
			name:     "gemini31 pro medium level",
			model:    "gemini-3.1-pro-preview",
			thinking: &core.Thinking{Mode: core.ThinkingEnabled, Effort: "medium"},
			want:     &thinkingConfig{ThinkingLevel: "medium", IncludeThoughts: core.Bool(true)},
		},
		{
			name:     "gemini3 disable floor",
			model:    "gemini-3-pro-preview",
			thinking: &core.Thinking{Mode: core.ThinkingDisabled},
			want:     &thinkingConfig{ThinkingLevel: "low", IncludeThoughts: core.Bool(false)},
		},
		{
			name:     "blank effort keeps default",
			model:    "gemini-2.5-flash",
			thinking: &core.Thinking{Mode: core.ThinkingEnabled, Effort: ""},
			want:     &thinkingConfig{IncludeThoughts: core.Bool(true)},
		},
		{
			// "auto" takes the same early-return path as "" in
			// convertThinking: no budget, no level, only thought
			// summaries. This guards against reviving the dead
			// case "", "auto" branch that thinkingBudgetFor used to
			// carry (it errored instead of keeping the default).
			name:     "auto effort keeps default",
			model:    "gemini-2.5-flash",
			thinking: &core.Thinking{Mode: core.ThinkingEnabled, Effort: "auto"},
			want:     &thinkingConfig{IncludeThoughts: core.Bool(true)},
		},
		{
			// Gemini 3 with auto effort also keeps the provider
			// default (no level) via the same early return.
			name:     "auto effort keeps default on gemini3",
			model:    "gemini-3-flash",
			thinking: &core.Thinking{Mode: core.ThinkingEnabled, Effort: "auto"},
			want:     &thinkingConfig{IncludeThoughts: core.Bool(true)},
		},
		{
			name:     "unknown effort",
			model:    "gemini-2.5-flash",
			thinking: &core.Thinking{Mode: core.ThinkingEnabled, Effort: "turbo"},
			wantErr:  "unknown thinking effort",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := convertThinking(tc.model, tc.thinking)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("convertThinking: %v", err)
			}
			if tc.want == nil {
				if got != nil {
					t.Fatalf("got %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("got nil, want %+v", tc.want)
			}
			if (tc.want.ThinkingBudget == nil) != (got.ThinkingBudget == nil) ||
				(tc.want.ThinkingBudget != nil && *got.ThinkingBudget != *tc.want.ThinkingBudget) {
				t.Fatalf("ThinkingBudget = %v, want %v", got.ThinkingBudget, tc.want.ThinkingBudget)
			}
			if got.ThinkingLevel != tc.want.ThinkingLevel {
				t.Fatalf("ThinkingLevel = %q, want %q", got.ThinkingLevel, tc.want.ThinkingLevel)
			}
			if (tc.want.IncludeThoughts == nil) != (got.IncludeThoughts == nil) ||
				(tc.want.IncludeThoughts != nil && *got.IncludeThoughts != *tc.want.IncludeThoughts) {
				t.Fatalf("IncludeThoughts = %v, want %v", got.IncludeThoughts, tc.want.IncludeThoughts)
			}
			if tc.wantWarningCode != "" {
				if len(warnings) != 1 || warnings[0].Code != tc.wantWarningCode {
					t.Fatalf("warnings = %+v, want one with code %q", warnings, tc.wantWarningCode)
				}
			} else if len(warnings) != 0 {
				t.Fatalf("warnings = %+v, want none", warnings)
			}
		})
	}
}

func TestBuildRequestGemini3BudgetReplacedWithWarning(t *testing.T) {
	provider, _ := New(Config{APIKey: "test"})
	req := &core.Request{
		Model:    "gemini-3-pro-preview",
		Thinking: &core.Thinking{Mode: core.ThinkingEnabled, Effort: "medium", BudgetTokens: core.IntPtr(5000)},
	}
	wire, warnings, err := provider.buildRequest(req, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if wire.GenerationConfig == nil || wire.GenerationConfig.ThinkingConfig == nil {
		t.Fatalf("thinking config = %+v", wire.GenerationConfig)
	}
	if wire.GenerationConfig.ThinkingConfig.ThinkingBudget != nil {
		t.Fatalf("thinkingBudget = %v, want nil on Gemini 3", *wire.GenerationConfig.ThinkingConfig.ThinkingBudget)
	}
	// medium is unsupported on gemini-3-pro-preview (only low/high), so the
	// clamp warning fires alongside the budget-replaced warning.
	if len(warnings) != 2 {
		t.Fatalf("warnings = %+v, want 2 (clamp + budget)", warnings)
	}
	codes := map[string]bool{}
	for _, w := range warnings {
		codes[w.Code] = true
	}
	if !codes["gemini.thinking_budget_unsupported"] || !codes["gemini.thinking_level_clamped"] {
		t.Fatalf("warning codes = %+v, want both budget_unsupported and level_clamped", codes)
	}
}

func TestBuildRequestToolsAndSchema(t *testing.T) {
	provider, _ := New(Config{APIKey: "test"})
	req := &core.Request{
		Model:    "gemini-2.5-flash",
		Messages: []core.Message{core.UserText("hi")},
		Tools: []core.Tool{
			mustTool(t, "lookup", "Look up data.", map[string]any{
				"$schema":              "http://json-schema.org/draft-07/schema",
				"type":                 "object",
				"additionalProperties": false,
				"strict":               true,
				"properties": map[string]any{
					"q":    map[string]any{"type": "string", "minLength": 1},
					"n":    map[string]any{"type": "integer", "format": "int64"},
					"tags": map[string]any{"type": "array"},
					"status": map[string]any{
						"type":   "string",
						"enum":   []any{"", "ok", "fail"},
						"format": "custom",
					},
				},
				"required": []string{"q"},
			}),
		},
		ToolChoice: "required",
	}
	wire, _, err := provider.buildRequest(req, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if len(wire.Tools) != 1 || len(wire.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("tools = %+v", wire.Tools)
	}
	declaration := wire.Tools[0].FunctionDeclarations[0]
	if declaration.Name != "lookup" || declaration.Description != "Look up data." {
		t.Fatalf("declaration = %+v", declaration)
	}
	var schema map[string]any
	if err := json.Unmarshal(declaration.Parameters, &schema); err != nil {
		t.Fatalf("parameters: %v", err)
	}
	if schema["additionalProperties"] != nil || schema["$schema"] != nil || schema["strict"] != nil {
		t.Fatalf("unsupported keywords survived: %+v", schema)
	}
	if schema["type"] != "OBJECT" {
		t.Fatalf("type = %v, want OBJECT", schema["type"])
	}
	properties := schema["properties"].(map[string]any)
	if properties["n"].(map[string]any)["type"] != "INTEGER" || properties["n"].(map[string]any)["format"] != nil {
		t.Fatalf("n schema = %+v, want INTEGER without int64 format", properties["n"])
	}
	tags := properties["tags"].(map[string]any)
	if tags["type"] != "ARRAY" || tags["items"].(map[string]any)["type"] != "OBJECT" {
		t.Fatalf("tags schema = %+v, want ARRAY with forced items", tags)
	}
	status := properties["status"].(map[string]any)
	if status["format"] != nil {
		t.Fatalf("status.format = %v, want dropped", status["format"])
	}
	enum := status["enum"].([]any)
	if len(enum) != 2 || enum[0] != "ok" || enum[1] != "fail" {
		t.Fatalf("enum = %+v, want empty string removed", status["enum"])
	}
	if wire.ToolConfig == nil || wire.ToolConfig.FunctionCallingConfig == nil || wire.ToolConfig.FunctionCallingConfig.Mode != "ANY" {
		t.Fatalf("toolConfig = %+v, want mode ANY for tool_choice required", wire.ToolConfig)
	}
}

func TestBuildRequestResponseFormat(t *testing.T) {
	provider, _ := New(Config{APIKey: "test"})
	req := &core.Request{
		Model:    "gemini-2.5-flash",
		Messages: []core.Message{core.UserText("json")},
		ResponseFormat: &core.ResponseFormat{
			Type: core.ResponseFormatJSONSchema,
			JSONSchema: &core.JSONSchema{
				Name: "ticker",
				Schema: mustSchema(t, map[string]any{
					"type":       "object",
					"properties": map[string]any{"symbol": map[string]any{"type": "string"}},
					"required":   []string{"symbol"},
				}),
			},
		},
	}
	wire, _, err := provider.buildRequest(req, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	cfg := wire.GenerationConfig
	if cfg == nil || cfg.ResponseMimeType != "application/json" {
		t.Fatalf("responseMimeType = %+v", cfg)
	}
	var schema map[string]any
	if err := json.Unmarshal(cfg.ResponseSchema, &schema); err != nil {
		t.Fatalf("responseSchema: %v", err)
	}
	if schema["type"] != "OBJECT" {
		t.Fatalf("schema type = %v", schema["type"])
	}
	ordering := schema["propertyOrdering"]
	if ordering == nil {
		t.Fatalf("propertyOrdering missing: %+v", schema)
	}
	if cfg.ThinkingConfig != nil {
		t.Fatalf("unexpected thinkingConfig: %+v", cfg.ThinkingConfig)
	}
	if cfg.Temperature != nil || cfg.TopP != nil || cfg.TopK != nil || cfg.MaxOutputTokens != nil {
		t.Fatalf("unexpected sampling fields: %+v", cfg)
	}
}

func mustSchema(t *testing.T, v any) core.Schema {
	t.Helper()
	schema, err := core.SchemaFrom(v)
	if err != nil {
		t.Fatalf("SchemaFrom: %v", err)
	}
	return schema
}

func TestBuildRequestToolChoiceNamed(t *testing.T) {
	provider, _ := New(Config{APIKey: "test"})
	req := &core.Request{
		Model:      "gemini-2.5-flash",
		Messages:   []core.Message{core.UserText("hi")},
		Tools:      []core.Tool{mustTool(t, "lookup", "", map[string]any{"type": "object"})},
		ToolChoice: map[string]any{"type": "function", "function": map[string]any{"name": "lookup"}},
	}
	wire, _, err := provider.buildRequest(req, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	cfg := wire.ToolConfig.FunctionCallingConfig
	if cfg.Mode != "ANY" || len(cfg.AllowedFunctionNames) != 1 || cfg.AllowedFunctionNames[0] != "lookup" {
		t.Fatalf("toolConfig = %+v", cfg)
	}
}

func TestBuildRequestPenalties(t *testing.T) {
	provider, _ := New(Config{APIKey: "test"})
	penalty := 0.5
	req25 := &core.Request{
		Model:            "gemini-2.5-flash",
		Messages:         []core.Message{core.UserText("hi")},
		FrequencyPenalty: &penalty,
		PresencePenalty:  &penalty,
	}
	wire, warnings, err := provider.buildRequest(req25, false)
	if err != nil {
		t.Fatalf("buildRequest(2.5): %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v", warnings)
	}
	cfg := wire.GenerationConfig
	if cfg == nil || cfg.FrequencyPenalty == nil || cfg.PresencePenalty == nil {
		t.Fatalf("penalties not forwarded on 2.x: %+v", cfg)
	}

	req3 := &core.Request{Model: "gemini-3-flash", FrequencyPenalty: &penalty, Messages: []core.Message{core.UserText("hi")}}
	wire3, warnings3, err := provider.buildRequest(req3, false)
	if err != nil {
		t.Fatalf("buildRequest(3): %v", err)
	}
	if len(warnings3) != 1 || warnings3[0].Code != "gemini.penalty_unsupported" {
		t.Fatalf("warnings = %+v", warnings3)
	}
	if wire3.GenerationConfig != nil && (wire3.GenerationConfig.FrequencyPenalty != nil || wire3.GenerationConfig.PresencePenalty != nil) {
		t.Fatalf("penalties survived on Gemini 3: %+v", wire3.GenerationConfig)
	}
}

func TestBuildRequestMedia(t *testing.T) {
	provider, _ := New(Config{APIKey: "test"})
	dataURL := "data:image/png;base64,iVBORw0KGgo="
	req := &core.Request{
		Model: "gemini-2.5-flash",
		Messages: []core.Message{
			core.User(
				core.TextBlock{Text: "look"},
				core.ImageBlock{Data: []byte("PNG"), MIME: "image/png"},
				core.ImageBlock{URL: dataURL},
				core.ImageBlock{URL: "https://example.com/cat.png"},
				core.ImageBlock{URL: "https://generativelanguage.googleapis.com/v1beta/files/abc"},
				core.ImageBlock{URL: "gs://bucket-name/path/to/image.png"},
				core.AudioBlock{Data: []byte("WAV"), MIME: "audio/wav"},
				core.VideoBlock{URL: "data:video/mp4;base64,AAAA"},
			),
		},
	}
	wire, _, err := provider.buildRequest(req, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	parts := wire.Contents[0].Parts
	if len(parts) != 8 {
		t.Fatalf("parts = %d, want 8", len(parts))
	}
	assertPart := func(index int, check func(p part) bool) {
		t.Helper()
		if !check(parts[index]) {
			t.Fatalf("part[%d] = %+v", index, parts[index])
		}
	}
	assertPart(1, func(p part) bool {
		return p.InlineData != nil && p.InlineData.MimeType == "image/png" && p.InlineData.Data == "UE5H"
	})
	assertPart(2, func(p part) bool {
		return p.InlineData != nil && p.InlineData.MimeType == "image/png" && p.InlineData.Data == "iVBORw0KGgo="
	})
	assertPart(3, func(p part) bool {
		return p.FileData != nil && p.FileData.FileURI == "https://example.com/cat.png" && p.FileData.MimeType == "image/png"
	})
	assertPart(4, func(p part) bool {
		return p.FileData != nil && p.FileData.FileURI == "https://generativelanguage.googleapis.com/v1beta/files/abc"
	})
	assertPart(5, func(p part) bool {
		return p.FileData != nil && p.FileData.FileURI == "gs://bucket-name/path/to/image.png" && p.FileData.MimeType == "image/png"
	})
	assertPart(6, func(p part) bool { return p.InlineData != nil && p.InlineData.MimeType == "audio/wav" })
	assertPart(7, func(p part) bool { return p.InlineData != nil && p.InlineData.MimeType == "video/mp4" })
}

func TestBuildRequestProviderOptions(t *testing.T) {
	provider, _ := New(Config{APIKey: "test"})
	req := &core.Request{
		Model:           "gemini-2.5-flash",
		Messages:        []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{"bogus": true},
	}
	if _, _, err := provider.buildRequest(req, false); err == nil || !strings.Contains(err.Error(), "unsupported provider option") {
		t.Fatalf("err = %v, want unsupported option error", err)
	}
	req.ProviderOptions = core.ProviderOptions{"session_id": "s-1", "prompt_cache_key": "k-1"}
	if _, _, err := provider.buildRequest(req, false); err != nil {
		t.Fatalf("transport-only options must be accepted: %v", err)
	}
}

func TestBuildRequestValidation(t *testing.T) {
	provider, _ := New(Config{APIKey: "test"})
	if _, _, err := provider.buildRequest(&core.Request{Messages: []core.Message{core.UserText("hi")}}, false); err == nil {
		t.Fatalf("missing model must error")
	}
	req := &core.Request{Model: "gemini-2.5-flash", Messages: []core.Message{core.UserText("hi")}, ToolChoice: "auto"}
	if _, _, err := provider.buildRequest(req, false); err == nil || !strings.Contains(err.Error(), "tool_choice requires at least one tool") {
		t.Fatalf("err = %v", err)
	}
}

func TestGenerateURL(t *testing.T) {
	cases := []struct {
		base   string
		model  string
		stream bool
		want   string
	}{
		{"", "gemini-2.5-flash", false, "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent"},
		{"https://generativelanguage.googleapis.com", "models/gemini-2.5-flash", false, "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent"},
		{"https://generativelanguage.googleapis.com/v1beta", "gemini/gemini-3-flash", false, "https://generativelanguage.googleapis.com/v1beta/models/gemini-3-flash:generateContent"},
		{"https://gateway.example.com/v1", "gemini-2.5-flash", true, "https://gateway.example.com/v1/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse"},
	}
	for _, tc := range cases {
		provider, err := New(Config{APIKey: "test", BaseURL: tc.base})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if got := provider.generateURL(tc.model, tc.stream); got != tc.want {
			t.Fatalf("generateURL(%q, %v) = %q, want %q", tc.model, tc.stream, got, tc.want)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestGemini3ThinkingLevelsTable(t *testing.T) {
	cases := []struct {
		model string
		want  []string
	}{
		{"gemini-3.8-flash", []string{"low", "medium", "high"}},
		{"gemini-3.7-flash", []string{"low", "medium", "high"}},
		{"gemini-3.6-flash", []string{"minimal", "low", "medium", "high"}},
		{"gemini-3.5-flash", []string{"minimal", "low", "medium", "high"}},
		{"gemini-3.5-flash-lite", []string{"minimal", "low", "medium", "high"}},
		{"gemini-3.1-flash-lite", []string{"minimal", "low", "medium", "high"}},
		{"gemini-3.1-flash-lite-image", []string{"minimal", "high"}},
		{"gemini-3-flash-preview", []string{"minimal", "low", "medium", "high"}},
		{"gemini-3-flash", []string{"minimal", "low", "medium", "high"}},
		{"gemini-3.1-pro-preview", []string{"low", "medium", "high"}},
		{"gemini-3-pro-preview", []string{"low", "high"}},
		// Unknown Gemini 3 model falls back to the conservative set.
		{"gemini-3-unknown-preview", []string{"low", "medium", "high"}},
		// Non-Gemini-3 models return nil.
		{"gemini-2.5-flash", nil},
		{"claude-3-opus", nil},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			got := gemini3ThinkingLevels(tc.model)
			if len(got) != len(tc.want) {
				t.Fatalf("gemini3ThinkingLevels(%q) = %v, want %v", tc.model, got, tc.want)
			}
			for i, s := range tc.want {
				if got[i] != s {
					t.Fatalf("gemini3ThinkingLevels(%q)[%d] = %q, want %q", tc.model, i, got[i], s)
				}
			}
		})
	}
}

func TestClampThinkingLevel(t *testing.T) {
	cases := []struct {
		name      string
		requested string
		supported []string
		want      string
		wantSub   bool
	}{
		// Supported level passes through.
		{"high on flash", "high", []string{"minimal", "low", "medium", "high"}, "high", false},
		{"minimal on flash-lite", "minimal", []string{"minimal", "low", "medium", "high"}, "minimal", false},
		// gemini-3-pro-preview {low, high}: minimal clamps down to low.
		{"pro minimal to low", "minimal", []string{"low", "high"}, "low", true},
		// gemini-3-pro-preview {low, high}: medium equidistant, picks high.
		{"pro medium to high", "medium", []string{"low", "high"}, "high", true},
		// gemini-3.8-flash {low, medium, high}: minimal clamps to low.
		{"3.8 minimal to low", "minimal", []string{"low", "medium", "high"}, "low", true},
		// gemini-3.1-flash-lite-image {minimal, high}: low clamps to minimal.
		{"image low to minimal", "low", []string{"minimal", "high"}, "minimal", true},
		// gemini-3.1-flash-lite-image {minimal, high}: medium clamps to high.
		{"image medium to high", "medium", []string{"minimal", "high"}, "high", true},
		// gemini-3.1-flash-lite-image {minimal, high}: high passes through.
		{"image high passthrough", "high", []string{"minimal", "high"}, "high", false},
		// gemini-3.1-flash-lite-image {minimal, high}: minimal passes through.
		{"image minimal passthrough", "minimal", []string{"minimal", "high"}, "minimal", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, sub := clampThinkingLevel(tc.requested, tc.supported)
			if got != tc.want || sub != tc.wantSub {
				t.Fatalf("clampThinkingLevel(%q, %v) = (%q, %v), want (%q, %v)",
					tc.requested, tc.supported, got, sub, tc.want, tc.wantSub)
			}
		})
	}
}

func TestConvertThinkingDisableFloorPerModel(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		// Models that support minimal use it as the disable floor.
		{"gemini-3-flash", "minimal"},
		{"gemini-3.5-flash", "minimal"},
		{"gemini-3.1-flash-lite-image", "minimal"},
		// Models without minimal use low as the floor.
		{"gemini-3-pro-preview", "low"},
		{"gemini-3.1-pro-preview", "low"},
		{"gemini-3.8-flash", "low"},
		// Unknown Gemini 3 defaults to {low, medium, high} → floor is low.
		{"gemini-3-new-unknown", "low"},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			cfg, warnings, err := convertThinking(tc.model, &core.Thinking{Mode: core.ThinkingDisabled})
			if err != nil {
				t.Fatalf("convertThinking: %v", err)
			}
			if cfg.ThinkingLevel != tc.want {
				t.Fatalf("disable floor = %q, want %q", cfg.ThinkingLevel, tc.want)
			}
			if *cfg.IncludeThoughts != false {
				t.Fatalf("includeThoughts = %v, want false", *cfg.IncludeThoughts)
			}
			if len(warnings) != 0 {
				t.Fatalf("disable must not warn: %+v", warnings)
			}
		})
	}
}

func TestConvertThinkingClampWarning(t *testing.T) {
	// gemini-3.1-flash-lite-image supports only {minimal, high}; requesting
	// medium clamps to high and emits a warning.
	cfg, warnings, err := convertThinking("gemini-3.1-flash-lite-image",
		&core.Thinking{Mode: core.ThinkingEnabled, Effort: "medium"})
	if err != nil {
		t.Fatalf("convertThinking: %v", err)
	}
	if cfg.ThinkingLevel != "high" {
		t.Fatalf("level = %q, want high (clamped from medium)", cfg.ThinkingLevel)
	}
	if len(warnings) != 1 || warnings[0].Code != "gemini.thinking_level_clamped" {
		t.Fatalf("warnings = %+v, want one clamp warning", warnings)
	}
	if !strings.Contains(warnings[0].Message, "medium") || !strings.Contains(warnings[0].Message, "high") {
		t.Fatalf("warning message = %q, want mention of medium→high", warnings[0].Message)
	}
}
