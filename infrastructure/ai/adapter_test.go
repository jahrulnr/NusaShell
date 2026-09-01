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
	"nusashell/infrastructure/ai/core"
	"nusashell/infrastructure/ai/openai"
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

func TestAdapterChatNoKeyOptional(t *testing.T) {
	// A chat-kind adapter with no API key must construct fine on
	// OpenAI-compatible hosts that need no auth (LM Studio, Ollama, vLLM,
	// OpenCode/Zen free tier); the vanilla OpenAI Chat adapter is used and
	// skips the Authorization header when no key is present.
	a := &Adapter{ProviderKind: domain.ProviderChat, OpenRouter: false, BaseURL: "https://opencode.ai/zen/v1"}
	p, err := a.providerFor()
	if err != nil {
		t.Fatalf("providerFor without key: %v", err)
	}
	if p.Name() != "openai" {
		t.Fatalf("adapter name = %q, want openai", p.Name())
	}

	// The openai provider must not reject keyless construction when
	// APIKeyOptional is set.
	if _, err := openai.New(openai.Config{BaseURL: "https://opencode.ai/zen/v1", APIKeyOptional: true}); err != nil {
		t.Fatalf("openai.New with APIKeyOptional: %v", err)
	}
	if _, err := openai.New(openai.Config{BaseURL: "https://opencode.ai/zen/v1"}); err == nil {
		t.Fatal("openai.New without APIKeyOptional must still require a key")
	}
}

func TestAdapterRouting(t *testing.T) {
	// Genuine OpenRouter host → OpenRouter adapter (wire: reasoning object).
	a := &Adapter{ProviderKind: domain.ProviderChat, OpenRouter: true, BaseURL: "https://openrouter.ai/api/v1", APIKey: "k"}
	p, err := a.providerFor()
	if err != nil {
		t.Fatalf("providerFor: %v", err)
	}
	if p.Name() != "openrouter" {
		t.Fatalf("adapter name = %q, want openrouter", p.Name())
	}

	// OpenAI-compatible aggregator (TokenRouter) → vanilla OpenAI Chat
	// adapter (wire: reasoning_effort). The OpenRouter flag is only set by
	// the factory for genuine OpenRouter hosts; even if it were set for an
	// aggregator, the provider stays on the vanilla wire.
	a = &Adapter{ProviderKind: domain.ProviderChat, OpenRouter: false, BaseURL: "https://api.tokenrouter.com/v1", APIKey: "k"}
	p, err = a.providerFor()
	if err != nil {
		t.Fatalf("providerFor: %v", err)
	}
	if p.Name() != "openai" {
		t.Fatalf("tokenrouter adapter name = %q, want openai", p.Name())
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

func TestListModelsParsesCanonicalSlug(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek/deepseek-chat","canonical_slug":"deepseek/deepseek-chat-v3","context_length":128000}]}`))
	}))
	defer srv.Close()

	ad := &Adapter{ProviderKind: domain.ProviderChat, BaseURL: srv.URL, Client: srv.Client()}
	models, err := ad.ListModels(context.Background(), "k")
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %+v", models)
	}
	if models[0].CanonicalSlug != "deepseek/deepseek-chat-v3" {
		t.Fatalf("canonical_slug = %q, want deepseek/deepseek-chat-v3", models[0].CanonicalSlug)
	}
	// Missing canonical_slug falls back to the model ID.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"plain-model"}]}`))
	}))
	defer srv2.Close()
	ad2 := &Adapter{ProviderKind: domain.ProviderChat, BaseURL: srv2.URL, Client: srv2.Client()}
	models, err = ad2.ListModels(context.Background(), "k")
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 || models[0].CanonicalSlug != "plain-model" {
		t.Fatalf("fallback canonical_slug = %+v", models)
	}
}

func TestAdapterListModelEndpoints(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"endpoints":[
			{"provider_name":"StreamLake","tag":"streamlake","quantization":"unknown","status":0,"latency_last_30m":1.25,"throughput_last_30m":50},
			{"provider_name":"DeepInfra","tag":"deepinfra/fp4","quantization":"fp4","status":-2,"latency_last_30m":null,"throughput_last_30m":null}
		]}}`))
	}))
	defer srv.Close()

	ad := &Adapter{
		ProviderKind: domain.ProviderChat,
		Driver:       domain.ProviderDriverOpenRouter,
		BaseURL:      srv.URL,
		APIKey:       "k",
		Client:       srv.Client(),
		OpenRouter:   true,
	}
	routes, err := ad.ListModelEndpoints(context.Background(), "deepseek/deepseek-chat-v3")
	if err != nil {
		t.Fatalf("ListModelEndpoints: %v", err)
	}
	if gotPath != "/models/deepseek/deepseek-chat-v3/endpoints" {
		t.Fatalf("request path = %q, want /models/deepseek/deepseek-chat-v3/endpoints", gotPath)
	}
	if len(routes) != 2 {
		t.Fatalf("routes = %+v", routes)
	}
	if routes[0].Slug != "streamlake" || routes[0].Name != "StreamLake" || routes[0].Status != 0 {
		t.Fatalf("route[0] = %+v", routes[0])
	}
	if routes[0].Latency == nil || *routes[0].Latency != 1.25 || routes[0].Throughput == nil || *routes[0].Throughput != 50 {
		t.Fatalf("route[0] metrics = %+v", routes[0])
	}
	if routes[1].Slug != "deepinfra/fp4" || routes[1].Quantization != "fp4" || routes[1].Latency != nil {
		t.Fatalf("route[1] = %+v", routes[1])
	}

	// Non-OpenRouter adapters report no routes without hitting the network.
	direct := &Adapter{ProviderKind: domain.ProviderChat, BaseURL: srv.URL, Client: srv.Client()}
	routes, err = direct.ListModelEndpoints(context.Background(), "x")
	if err != nil {
		t.Fatalf("direct ListModelEndpoints: %v", err)
	}
	if len(routes) != 0 {
		t.Fatalf("direct routes = %+v, want empty", routes)
	}
}
