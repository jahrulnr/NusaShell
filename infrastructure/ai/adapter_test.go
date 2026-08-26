package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nusashell/application"
	"nusashell/domain"
	"nusashell/infrastructure/ai/compat"
	"nusashell/infrastructure/ai/core"
)

func TestToCoreRequestSystemAndMessages(t *testing.T) {
	req := application.ChatRequest{
		Model: "m", MaxTokens: 64, System: "sys",
		Messages: []application.ChatMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello", ToolCalls: []domain.ToolCall{{ID: "c1", Name: "lookup", Args: `{"q":"x"}`}}},
			{Role: "tool", ToolResult: &application.ToolResult{ToolCallID: "c1", Content: "result"}},
		},
	}
	lr := application.ToCoreRequest(req, domain.ProviderChat, false)
	if lr.Model != "m" || lr.MaxTokens == nil || *lr.MaxTokens != 64 {
		t.Fatalf("model/max = %q/%v", lr.Model, lr.MaxTokens)
	}
	if len(lr.Messages) != 4 {
		t.Fatalf("messages = %d, want 4 (system+user+assistant+tool)", len(lr.Messages))
	}
	if lr.Messages[0].Role != core.RoleSystem {
		t.Fatalf("first message role = %q", lr.Messages[0].Role)
	}
	asst := lr.Messages[2]
	if asst.Role != core.RoleAssistant || len(asst.Blocks) != 2 {
		t.Fatalf("assistant = %+v", asst)
	}
	toolMsg := lr.Messages[3]
	if toolMsg.Role != core.RoleTool {
		t.Fatalf("tool message role = %q", toolMsg.Role)
	}
}

func TestToCoreRequestAttachments(t *testing.T) {
	req := application.ChatRequest{
		Model: "m", MaxTokens: 8,
		Messages: []application.ChatMessage{{
			Role: "user", Content: "see",
			Attachments: []domain.Attachment{
				{Type: "image", Name: "p.png", MediaType: "image/png", DataURL: "data:image/png;base64,aGVsbG8="},
				{Type: "audio", Name: "a.mp3", MediaType: "audio/mpeg", DataURL: "data:audio/mpeg;base64,YXVkaW8="},
				{Type: "video", Name: "v.mp4", MediaType: "video/mp4", DataURL: "data:video/mp4;base64,dmlkZW8="},
				{Type: "file", Name: "d.pdf", MediaType: "application/pdf", DataURL: "data:application/pdf;base64,cGRm"},
				{Type: "text", Name: "n.txt", MediaType: "text/plain", Content: "note"},
			},
		}},
	}
	lr := application.ToCoreRequest(req, domain.ProviderChat, false)
	blocks := lr.Messages[0].Blocks
	kinds := map[string]bool{}
	for _, b := range blocks {
		switch b.(type) {
		case core.ImageBlock:
			kinds["image"] = true
		case core.AudioBlock:
			kinds["audio"] = true
		case core.VideoBlock:
			kinds["video"] = true
		case core.TextBlock:
			kinds["text"] = true
		}
	}
	for _, want := range []string{"image", "audio", "video", "text"} {
		if !kinds[want] {
			t.Fatalf("missing %s block in %T list", want, blocks)
		}
	}
	// file attachment folds into a text block, not a distinct kind
	if kinds["file"] {
		t.Fatal("file must not map to its own block kind")
	}
}

func TestToCoreRequestEffortAndStrip(t *testing.T) {
	effort := "high"
	temp := 0.7
	lr := application.ToCoreRequest(application.ChatRequest{
		Model: "m", MaxTokens: 8, Effort: effort, Temperature: &temp,
	}, domain.ProviderChat, false)
	if lr.Thinking == nil || lr.Thinking.Mode != core.ThinkingEnabled || lr.Thinking.Effort != "high" {
		t.Fatalf("thinking = %+v", lr.Thinking)
	}
	if lr.Temperature == nil || *lr.Temperature != 0.7 {
		t.Fatalf("temperature = %v", lr.Temperature)
	}

	// strip params null the sampling fields
	lr = application.ToCoreRequest(application.ChatRequest{
		Model: "m", MaxTokens: 8, Effort: "high", Temperature: &temp,
		StripParams: []string{"temperature", "reasoning_effort"},
	}, domain.ProviderChat, false)
	if lr.Temperature != nil {
		t.Fatalf("temperature must be stripped, got %v", lr.Temperature)
	}
	if lr.Thinking != nil {
		t.Fatalf("thinking must be stripped with reasoning_effort, got %+v", lr.Thinking)
	}
}

func TestThinkingFromEffort(t *testing.T) {
	req := application.ChatRequest{Effort: ""}
	lr := application.ToCoreRequest(req, domain.ProviderChat, false)
	if lr.Thinking != nil {
		t.Fatalf("empty effort = %+v, want nil", lr.Thinking)
	}
	req.Effort = "auto"
	lr = application.ToCoreRequest(req, domain.ProviderChat, false)
	if lr.Thinking != nil {
		t.Fatalf("auto effort = %+v, want nil", lr.Thinking)
	}
	req.Effort = "none"
	lr = application.ToCoreRequest(req, domain.ProviderChat, false)
	if lr.Thinking == nil || lr.Thinking.Mode != core.ThinkingDisabled {
		t.Fatalf("none effort = %+v, want disabled", lr.Thinking)
	}
	req.Effort = "low"
	lr = application.ToCoreRequest(req, domain.ProviderChat, false)
	if lr.Thinking == nil || lr.Thinking.Mode != core.ThinkingEnabled || lr.Thinking.Effort != "low" {
		t.Fatalf("low effort = %+v", lr.Thinking)
	}
}

func TestMapErrorHTTP(t *testing.T) {
	err := application.MapCoreError(core.NewHTTPError("openai", 429, `{"error":{"message":"rate limited"}}`), domain.ProviderChat)
	var up *application.UpstreamError
	if !errors.As(err, &up) {
		t.Fatalf("error = %T, want *UpstreamError", err)
	}
	if up.Kind != application.KindHTTPStatus || up.StatusCode != 429 {
		t.Fatalf("kind/status = %q/%d", up.Kind, up.StatusCode)
	}
	if up.Temporary {
		t.Fatal("429 without Retry-After must not be Temporary (fail fast)")
	}
	if !strings.Contains(err.Error(), "HTTP 429") {
		t.Fatalf("error text must keep HTTP status, got %q", err.Error())
	}
}

func TestMapErrorHTTPWithRetryAfter(t *testing.T) {
	err := application.MapCoreError(&core.LiteLLMError{Type: core.ErrorTypeRateLimit, StatusCode: 429, RetryAfter: 60, Message: "slow down", Retryable: true}, domain.ProviderChat)
	var up *application.UpstreamError
	if !errors.As(err, &up) {
		t.Fatalf("error = %T, want *UpstreamError", err)
	}
	if !up.Temporary {
		t.Fatal("429 with Retry-After must be Temporary")
	}
	if up.RetryAfter.Seconds() != 60 {
		t.Fatalf("RetryAfter = %v", up.RetryAfter)
	}
}

func TestMapErrorNetwork(t *testing.T) {
	err := application.MapCoreError(core.NewNetworkError("openai", "boom", errors.New("dial tcp: refused")), domain.ProviderChat)
	var up *application.UpstreamError
	if !errors.As(err, &up) {
		t.Fatalf("error = %T, want *UpstreamError", err)
	}
	if up.Kind != application.KindConnect || !up.Temporary {
		t.Fatalf("kind/temporary = %q/%v", up.Kind, up.Temporary)
	}
}

func TestMapErrorPassesThroughNonLiteLLM(t *testing.T) {
	inner := errors.New("plain")
	if got := application.MapCoreError(inner, domain.ProviderChat); got != inner {
		t.Fatalf("plain error must pass through, got %v", got)
	}
}

func TestAdapterKind(t *testing.T) {
	for _, kind := range []domain.ProviderKind{domain.ProviderMessages, domain.ProviderResponses, domain.ProviderChat} {
		a := &Adapter{ProviderKind: kind}
		if a.ProviderKind != kind {
			t.Fatalf("ProviderKind = %q, want %q", a.ProviderKind, kind)
		}
	}
}

func TestAdapterOpenRouterNoKeyOptional(t *testing.T) {
	// A chat-kind OpenRouter adapter with no API key must construct fine
	// (free-tier hosts like OpenCode/Zen accept keyless requests); the
	// upstream decides. With a key, construction still succeeds.
	a := &Adapter{ProviderKind: domain.ProviderChat, OpenRouter: true, BaseURL: "https://opencode.ai/zen/v1"}
	p, err := a.providerFor()
	if err != nil {
		t.Fatalf("providerFor without key: %v", err)
	}
	if p.Name() != "openrouter" {
		t.Fatalf("adapter name = %q, want openrouter", p.Name())
	}

	// The compat provider must not reject keyless construction when
	// APIKeyOptional is set.
	if _, err := compat.New(compat.Config{BaseURL: "https://opencode.ai/zen/v1", APIKeyOptional: true}, compat.Spec{Name: "openrouter", Auth: compat.AuthSpec{APIKeyRequired: true}}); err != nil {
		t.Fatalf("compat.New with APIKeyOptional: %v", err)
	}
	if _, err := compat.New(compat.Config{BaseURL: "https://opencode.ai/zen/v1"}, compat.Spec{Name: "openrouter", Auth: compat.AuthSpec{APIKeyRequired: true}}); err == nil {
		t.Fatal("compat.New without APIKeyOptional must still require a key")
	}
}

func TestAdapterOpenRouterDefault(t *testing.T) {
	// Chat-kind with a non-OpenAI host defaults to the OpenRouter adapter
	// (aggregators like TokenRouter, OmniRoute, OpenCode speak the
	// OpenRouter wire format). The factory sets OpenRouter=true for
	// chat-kind non-OpenAI hosts.
	a := &Adapter{ProviderKind: domain.ProviderChat, OpenRouter: true, BaseURL: "https://api.tokenrouter.com/v1", APIKey: "k"}
	p, err := a.providerFor()
	if err != nil {
		t.Fatalf("providerFor: %v", err)
	}
	if p.Name() != "openrouter" {
		t.Fatalf("adapter name = %q, want openrouter", p.Name())
	}

	// Chat-kind with api.openai.com stays on the vanilla OpenAI chat adapter.
	a = &Adapter{ProviderKind: domain.ProviderChat, OpenRouter: false, BaseURL: "https://api.openai.com/v1", APIKey: "k"}
	p, err = a.providerFor()
	if err != nil {
		t.Fatalf("providerFor: %v", err)
	}
	if p.Name() != "openai" {
		t.Fatalf("openai direct adapter name = %q, want openai", p.Name())
	}
}

func TestExplicitOpenRouterDriverHonorsAPIKind(t *testing.T) {
	var lastPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	tests := []struct {
		kind domain.ProviderKind
		path string
	}{
		{kind: domain.ProviderChat, path: "/v1/chat/completions"},
		{kind: domain.ProviderResponses, path: "/v1/responses"},
		{kind: domain.ProviderMessages, path: "/v1/messages"},
	}
	for _, tc := range tests {
		t.Run(string(tc.kind), func(t *testing.T) {
			maxTokens := 32
			adapter := &Adapter{
				Driver:       domain.ProviderDriverOpenRouter,
				ProviderKind: tc.kind,
				BaseURL:      srv.URL + "/v1",
				APIKey:       "key",
				Client:       srv.Client(),
			}
			provider, err := adapter.providerFor()
			if err != nil {
				t.Fatalf("providerFor: %v", err)
			}
			stream, err := provider.Stream(context.Background(), &core.Request{
				Model: "model", MaxTokens: &maxTokens,
				Messages: []core.Message{{
					Role:   core.RoleUser,
					Blocks: []core.Block{core.TextBlock{Text: "hello"}},
				}},
			})
			if err != nil {
				t.Fatalf("stream: %v", err)
			}
			if err := stream.Close(); err != nil {
				t.Fatalf("close stream: %v", err)
			}
			if lastPath != tc.path {
				t.Fatalf("request path = %q, want %q", lastPath, tc.path)
			}
		})
	}
}

func TestListModelsRoutesAnthropicAndOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-x","display_name":"Claude X","context_window":200000,"pricing":{"input":"3.0"}}]}`))
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-x","context_length":128000,"max_tokens":8000,"pricing":{"prompt":"1.5"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	an := &Adapter{ProviderKind: domain.ProviderMessages, BaseURL: srv.URL, Client: srv.Client()}
	models, err := an.ListModels(context.Background(), "k")
	if err != nil {
		t.Fatalf("anthropic ListModels: %v", err)
	}
	if len(models) != 1 || models[0].ID != "claude-x" || models[0].Context != 200000 {
		t.Fatalf("anthropic models = %+v", models)
	}

	oa := &Adapter{ProviderKind: domain.ProviderChat, BaseURL: srv.URL, Client: srv.Client()}
	models, err = oa.ListModels(context.Background(), "k")
	if err != nil {
		t.Fatalf("openai ListModels: %v", err)
	}
	if len(models) != 1 || models[0].ID != "gpt-x" || models[0].Context != 128000 {
		t.Fatalf("openai models = %+v", models)
	}
}
