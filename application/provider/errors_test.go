package provider

import (
	"strings"
	"testing"

	"nusashell/domain"
)

// ── 400 error classification tests (moved from domain/learned_params_test.go) ──

func TestClassify400ErrorUnsupportedParam(t *testing.T) {
	cases := []struct {
		body   string
		action domain.LearnedParamAction
		param  string
	}{
		{`{"error":{"message":"Unsupported parameter: logprobs"}}`, domain.LearnedActionStrip, "logprobs"},
		{`Unsupported parameter(s): 'reasoning_budget'`, domain.LearnedActionStrip, "reasoning_budget"},
		{`Unsupported parameter 'top_logprobs'`, domain.LearnedActionStrip, "top_logprobs"},
		{`unsupported parameter: verbosity`, domain.LearnedActionStrip, "verbosity"},
		// "Unknown parameter" is the phrasing OpenAI-compatible aggregators
		// use — TokenRouter rejects the OpenRouter reasoning object with
		// "Unknown parameter: 'reasoning'". It must be learned exactly like
		// "Unsupported parameter" so the strip-and-retry loop can recover.
		{`{"error":{"message":"Unknown parameter: 'reasoning'"}}`, domain.LearnedActionStrip, "reasoning"},
		{`unknown_parameter: Unknown parameter: 'reasoning'.`, domain.LearnedActionStrip, "reasoning"},
		{`Unknown parameter: reasoning`, domain.LearnedActionStrip, "reasoning"},
	}
	for _, c := range cases {
		action, param := Classify400Error(c.body)
		if action != c.action || param != c.param {
			t.Errorf("Classify400Error(%q) = (%q, %q), want (%q, %q)", c.body, action, param, c.action, c.param)
		}
	}
}

func TestClassify400ErrorRequiredField(t *testing.T) {
	cases := []struct {
		body   string
		action domain.LearnedParamAction
		param  string
	}{
		{`reasoning_content must be passed back`, domain.LearnedActionInject, "reasoning_content"},
		{`field 'reasoning_content' is required`, domain.LearnedActionInject, "reasoning_content"},
		{`reasoning_content is a required field`, domain.LearnedActionInject, "reasoning_content"},
		{`reasoning_content must be provided`, domain.LearnedActionInject, "reasoning_content"},
		// OpenCode Console Go wraps the field in backticks and inserts
		// "in the thinking mode" between the name and "must be passed back".
		// The classifier must capture reasoning_content, not the noun "mode".
		{"The `reasoning_content` in the thinking mode must be passed back to the API.", domain.LearnedActionInject, "reasoning_content"},
		{"provider returned HTTP 400: invalid_request_error: Error from provider (Console Go): Upstream request failed: [invalid_request_error] The `reasoning_content` in the thinking mode must be passed back to the API.", domain.LearnedActionInject, "reasoning_content"},
	}
	for _, c := range cases {
		action, param := Classify400Error(c.body)
		if action != c.action || param != c.param {
			t.Errorf("Classify400Error(%q) = (%q, %q), want (%q, %q)", c.body, action, param, c.action, c.param)
		}
	}
}

func TestClassify400ErrorEmptyOrUnknown(t *testing.T) {
	cases := []string{"", "internal server error", `{"error":"rate limit"}`, "model not found"}
	for _, body := range cases {
		action, param := Classify400Error(body)
		if action != "" || param != "" {
			t.Errorf("Classify400Error(%q) = (%q, %q), want empty", body, action, param)
		}
	}
}

func TestClassify400ErrorNoUserQuery(t *testing.T) {
	cases := []struct {
		body   string
		action domain.LearnedParamAction
		param  string
	}{
		{`bad_response_status_code: No user query found in messages.`, domain.LearnedActionNudgeUser, "user_message"},
		{`No user query found in messages.`, domain.LearnedActionNudgeUser, "user_message"},
		{`{"error":{"message":"No user query found in messages."}}`, domain.LearnedActionNudgeUser, "user_message"},
		{`no user query found in messages`, domain.LearnedActionNudgeUser, "user_message"},
		{`No user message found in the conversation`, domain.LearnedActionNudgeUser, "user_message"},
		{`Messages must contain at least one user message`, domain.LearnedActionNudgeUser, "user_message"},
	}
	for _, c := range cases {
		action, param := Classify400Error(c.body)
		if action != c.action || param != c.param {
			t.Errorf("Classify400Error(%q) = (%q, %q), want (%q, %q)", c.body, action, param, c.action, c.param)
		}
	}
}

func TestClassify400ErrorAssistantPrefill(t *testing.T) {
	body := `provider returned HTTP 400: {"error":{"message":"This model does not support assistant message prefill. The conversation must end with a user message."}}`
	action, param := Classify400Error(body)
	if action != domain.LearnedActionNudgeUser || param != "user_message" {
		t.Fatalf("Classify400Error(prefill) = (%q, %q), want (%q, %q)", action, param, domain.LearnedActionNudgeUser, "user_message")
	}
}

// TestClassify400ErrorTextOnly proves that "text-only" / "must be a text
// part" errors from text-only models (e.g. Qwen3.8) are classified as
// LearnedActionDisableModality with param "vision" — the most common
// non-text modality that triggers this error. The retry loop will set
// caps.Vision=false and strip images on retry.
func TestClassify400ErrorTextOnly(t *testing.T) {
	cases := []struct {
		body  string
		param string
	}{
		{
			`Qwen3.8 open checkpoint is text-only; messages[131].content[1] must be a text part`,
			"vision",
		},
		{
			`{"error":{"message":"model is text-only; messages[5].content[2] must be a text part"}}`,
			"vision",
		},
		{
			`this model is text-only and cannot process images`,
			"vision",
		},
	}
	for _, c := range cases {
		action, param := Classify400Error(c.body)
		if action != domain.LearnedActionDisableModality {
			t.Errorf("Classify400Error(%q) action = %q, want %q", c.body, action, domain.LearnedActionDisableModality)
		}
		if param != c.param {
			t.Errorf("Classify400Error(%q) param = %q, want %q", c.body, param, c.param)
		}
	}
}

// Required-field pattern must win over unsupported-param when both could
// match (a model can reject reasoning_content as unsupported on one
// endpoint while requiring it on another).
func TestClassify400ErrorRequiredWinsOverUnsupported(t *testing.T) {
	body := `reasoning_content is required: unsupported parameter reasoning_content`
	action, param := Classify400Error(body)
	if action != domain.LearnedActionInject {
		t.Errorf("expected inject, got %q (body should match required-field first)", action)
	}
	if param != "reasoning_content" {
		t.Errorf("expected reasoning_content, got %q", param)
	}
}

func TestClassify400ErrorCapContext(t *testing.T) {
	cases := []struct {
		body   string
		action domain.LearnedParamAction
		param  string
	}{
		{
			`Requested token count exceeds the model's maximum context length of 262144 tokens. You requested a total of 267042 tokens: 201506 tokens from the input messages and 65536 tokens for the completion.`,
			domain.LearnedActionCapContext,
			"262144",
		},
		{
			`This model's maximum context length is 8192 tokens, however you requested 9000 tokens.`,
			domain.LearnedActionCapContext,
			"8192",
		},
		{
			`{"error":{"message":"context length of 128,000 exceeded"}}`,
			domain.LearnedActionCapContext,
			"128000",
		},
		{
			`context_length_exceeded`,
			"",
			"",
		},
		{
			`You requested 267042 tokens`,
			"",
			"",
		},
	}
	for _, c := range cases {
		action, param := Classify400Error(c.body)
		if action != c.action || param != c.param {
			t.Errorf("Classify400Error(%q) = (%q, %q), want (%q, %q)", c.body, action, param, c.action, c.param)
		}
	}
}

func TestExtractContextLimit(t *testing.T) {
	tests := []struct {
		body  string
		limit int
		num   string
		ok    bool
	}{
		{
			`Requested token count exceeds the model's maximum context length of 262144 tokens.`,
			262144,
			"262144",
			true,
		},
		{
			`maximum context window is 1_000_000`,
			1000000,
			"1000000",
			true,
		},
		{"no numbers here", 0, "", false},
	}
	for _, tt := range tests {
		limit, num, ok := ExtractContextLimit(tt.body)
		if ok != tt.ok {
			t.Errorf("ExtractContextLimit(%q) ok = %t, want %t", tt.body, ok, tt.ok)
		}
		if ok && (limit != tt.limit || num != tt.num) {
			t.Errorf("ExtractContextLimit(%q) = (%d, %q), want (%d, %q)", tt.body, limit, num, tt.limit, tt.num)
		}
	}
}

// TestClassify400ErrorGeminiThoughtSignature reproduces the real Gemini 400
// body that previously produced the garbage param "this". The required field
// (thought_signature) is mentioned earlier in the sentence; the word right
// before "is required" is the pronoun "This". The classifier must capture
// thought_signature, not the pronoun.
func TestClassify400ErrorGeminiThoughtSignature(t *testing.T) {
	body := "provider returned HTTP 400: {\n  \"error\": {\n    \"code\": 400,\n    " +
		"\"message\": \"Function call is missing a thought_signature in functionCall parts. " +
		"This is required for tools to work correctly, and missing thought_signatures may " +
		"lead to degraded performance or errors.\",\n    \"status\": \"INVALID_ARGUMENT\"\n  }\n}"
	action, param := Classify400Error(body)
	if action != domain.LearnedActionInject {
		t.Errorf("action = %q, want %q", action, domain.LearnedActionInject)
	}
	if param != "thought_signature" {
		t.Errorf("param = %q, want %q (must not capture the pronoun \"this\")", param, "thought_signature")
	}
}

// A body whose only "is required" subject is a pronoun must not be learned
// as a param. Without a real field name the classifier returns empty rather
// than recording garbage like "this".
func TestClassify400ErrorPronounNotCaptured(t *testing.T) {
	cases := []string{
		`This is required for the request to succeed`,
		`That must be provided before continuing`,
		`It is required`,
	}
	for _, body := range cases {
		action, param := Classify400Error(body)
		if domain.IsParamStopword(param) {
			t.Errorf("Classify400Error(%q) captured stopword param %q", body, param)
		}
		if param == "this" || param == "that" || param == "it" {
			t.Errorf("Classify400Error(%q) = (%q, %q), must not capture a pronoun", body, action, param)
		}
	}
}

// TestClassify400ErrorMissingField covers the "missing <field>" family of
// errors (Gemini-style) where the required field follows the word "missing".
func TestClassify400ErrorMissingField(t *testing.T) {
	cases := []struct {
		body  string
		param string
	}{
		{`Function call is missing a thought_signature in functionCall parts`, "thought_signature"},
		{`missing required field 'reasoning_content'`, "reasoning_content"},
		{`missing parameter api_key`, "api_key"},
		{`The request is missing an auth_token`, "auth_token"},
	}
	for _, c := range cases {
		action, param := Classify400Error(c.body)
		if action != domain.LearnedActionInject {
			t.Errorf("Classify400Error(%q) action = %q, want %q", c.body, action, domain.LearnedActionInject)
		}
		if param != c.param {
			t.Errorf("Classify400Error(%q) param = %q, want %q", c.body, param, c.param)
		}
	}
}

// TestExtractContextLimitTruncation is a sanity check that the truncation
// marker is not part of the numeric output.
func TestExtractContextLimitNoTruncationMarker(t *testing.T) {
	body := strings.Repeat("x", 300) + " context length of 8192 tokens"
	limit, num, ok := ExtractContextLimit(body)
	if !ok || limit != 8192 || num != "8192" {
		t.Fatalf("ExtractContextLimit = (%d, %q, %t), want (8192, 8192, true)", limit, num, ok)
	}
}
