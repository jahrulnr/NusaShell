package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nusashell/application"
	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
	"nusashell/infrastructure/ai/openrouter"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

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
	var up *domain.ProviderError
	if !errors.As(err, &up) {
		t.Fatalf("error = %T, want *domain.ProviderError", err)
	}
	if up.Kind != domain.KindHTTPStatus || up.StatusCode != 429 {
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
	var up *domain.ProviderError
	if !errors.As(err, &up) {
		t.Fatalf("error = %T, want *domain.ProviderError", err)
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
	var up *domain.ProviderError
	if !errors.As(err, &up) {
		t.Fatalf("error = %T, want *domain.ProviderError", err)
	}
	if up.Kind != domain.KindConnect || !up.Temporary {
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

func TestAdapterCodexProviderRouting(t *testing.T) {
	a := &Adapter{
		ProviderKind: domain.ProviderCodex,
		Driver:       domain.ProviderDriverCodex,
		BaseURL:      "https://chatgpt.com/backend-api/codex",
		APIKey:       "access-token",
	}
	provider, err := a.providerFor()
	if err != nil {
		t.Fatalf("providerFor: %v", err)
	}
	if provider.Name() != "codex" {
		t.Fatalf("provider name = %q, want codex", provider.Name())
	}
}

func TestAdapterChatNoKeyOptional(t *testing.T) {
	// A custom chat provider uses the OpenRouter compatibility/profile path,
	// even when its gateway is not hosted at openrouter.ai. No API key is
	// still allowed for local and gateway hosts that permit unauthenticated
	// requests. OpenCode is intentionally tested separately as a vanilla Chat
	// wire exception.
	a := &Adapter{ProviderKind: domain.ProviderChat, OpenRouter: true, BaseURL: "https://api.tokenrouter.com/v1"}
	p, err := a.providerFor()
	if err != nil {
		t.Fatalf("providerFor without key: %v", err)
	}
	if p.Name() != "openrouter" {
		t.Fatalf("adapter name = %q, want openrouter", p.Name())
	}

	// The OpenRouter compatibility provider must not reject keyless
	// construction when APIKeyOptional is set.
	if _, err := openrouter.New(openrouter.Config{BaseURL: "https://opencode.ai/zen/v1", APIKeyOptional: true}); err != nil {
		t.Fatalf("openrouter.New with APIKeyOptional: %v", err)
	}
	if _, err := openrouter.New(openrouter.Config{BaseURL: "https://opencode.ai/zen/v1"}); err == nil {
		t.Fatal("openrouter.New without APIKeyOptional must still require a key")
	}
}

func TestAdapterAllKindsNoKeyOptional(t *testing.T) {
	base := "http://127.0.0.1:4096"
	cases := []Adapter{
		{ProviderKind: domain.ProviderMessages, Driver: domain.ProviderDriverOpenRouter, BaseURL: base},
		{ProviderKind: domain.ProviderResponses, Driver: domain.ProviderDriverOpenRouter, BaseURL: base + "/v1"},
		{ProviderKind: domain.ProviderChat, Driver: domain.ProviderDriverOpenRouter, BaseURL: base + "/v1"},
		{ProviderKind: domain.ProviderMessages, Driver: domain.ProviderDriverAnthropic, BaseURL: base},
		{ProviderKind: domain.ProviderResponses, Driver: domain.ProviderDriverOpenAI, BaseURL: base + "/v1"},
		{ProviderKind: domain.ProviderChat, OpenRouter: false, BaseURL: base + "/v1"},
	}
	for _, a := range cases {
		if _, err := a.providerFor(); err != nil {
			t.Errorf("kind=%s driver=%q: providerFor without key: %v", a.ProviderKind, a.Driver, err)
		}
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

	// Custom OpenAI-compatible aggregators also use the OpenRouter
	// compatibility/profile adapter. The target URL remains the custom
	// gateway, so it can accept the OpenRouter-compatible request shape.
	a = &Adapter{ProviderKind: domain.ProviderChat, OpenRouter: true, BaseURL: "https://api.tokenrouter.com/v1", APIKey: "k"}
	p, err = a.providerFor()
	if err != nil {
		t.Fatalf("providerFor: %v", err)
	}
	if p.Name() != "openrouter" {
		t.Fatalf("tokenrouter adapter name = %q, want openrouter", p.Name())
	}

	// OpenCode is a protocol exception: the stored OpenRouter driver still
	// uses vanilla OpenAI Chat so reasoning history is sent as
	// reasoning_content, which Console Go requires.
	a = &Adapter{
		ProviderKind: domain.ProviderChat,
		Driver:       domain.ProviderDriverOpenRouter,
		OpenRouter:   false,
		BaseURL:      "https://opencode.ai/zen/go/v1",
		APIKey:       "k",
	}
	p, err = a.providerFor()
	if err != nil {
		t.Fatalf("opencode providerFor: %v", err)
	}
	if p.Name() != "openai" {
		t.Fatalf("opencode+openrouter-driver adapter name = %q, want openai", p.Name())
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

func TestOpenCodeExplicitOpenRouterDriverUsesReasoningContentWire(t *testing.T) {
	var body map[string]any
	var requestPath string
	var decodeErr error
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		requestPath = req.URL.Path
		decodeErr = json.NewDecoder(req.Body).Decode(&body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"model":"deepseek-chat","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)),
			Request:    req,
		}, nil
	})}
	baseURL := "https://opencode.ai/zen/go/v1"
	a := &Adapter{
		ProviderKind: domain.ProviderChat,
		Driver:       domain.ProviderDriverOpenRouter,
		OpenRouter:   domain.UsesOpenRouterWire(domain.ProviderChat, domain.ProviderDriverOpenRouter, baseURL),
		BaseURL:      baseURL,
		APIKey:       "k",
		Client:       client,
	}
	if a.OpenRouter {
		t.Fatal("OpenCode Chat must select the vanilla wire even when the stored driver is OpenRouter")
	}
	_, err := a.Chat(context.Background(), &core.Request{
		Model:    "deepseek-chat",
		Thinking: &core.Thinking{Mode: core.ThinkingEnabled, Effort: "medium"},
		Messages: []core.Message{
			core.UserText("continue"),
			core.Assistant(core.ReasoningBlock{Text: "prior reasoning"}),
			core.UserText("next"),
		},
	})
	if err != nil {
		t.Fatalf("OpenCode Chat: %v", err)
	}
	if requestPath != "/zen/go/v1/chat/completions" {
		t.Fatalf("request path = %q, want /zen/go/v1/chat/completions", requestPath)
	}
	if decodeErr != nil {
		t.Fatalf("request JSON: %v", decodeErr)
	}
	messages, ok := body["messages"].([]any)
	if !ok || len(messages) != 3 {
		t.Fatalf("messages = %#v, want three Chat messages", body["messages"])
	}
	assistant, ok := messages[1].(map[string]any)
	if !ok {
		t.Fatalf("assistant message = %#v, want object", messages[1])
	}
	if got := assistant["reasoning_content"]; got != "prior reasoning" {
		t.Fatalf("assistant reasoning_content = %#v, want prior reasoning", got)
	}
	for _, field := range []string{"reasoning", "reasoning_details"} {
		if _, exists := assistant[field]; exists {
			t.Fatalf("assistant must not send OpenRouter %s field: %#v", field, assistant)
		}
	}
}

func TestNewFactoryCustomChatUsesOpenRouterVideoMapping(t *testing.T) {
	var rawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"custom-video","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	factory := NewFactory(&stubCreds{})
	provider, err := factory(context.Background(), &domain.Provider{
		ID:      "prov_custom_video",
		Driver:  domain.ProviderDriverOpenRouter,
		Kind:    domain.ProviderChat,
		BaseURL: srv.URL + "/v1",
	}, "key")
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	adapter, ok := provider.(*Adapter)
	if !ok {
		t.Fatalf("provider = %T, want *Adapter", provider)
	}
	_, err = adapter.Chat(context.Background(), &core.Request{
		Model: "custom-video",
		Messages: []core.Message{core.User(
			core.Text("inspect this video"),
			core.VideoBlock{URL: "data:video/mp4;base64,AAAA"},
		)},
	})
	if err != nil {
		t.Fatalf("custom Chat: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(rawBody, &body); err != nil {
		t.Fatalf("request JSON: %v", err)
	}
	messages, ok := body["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %#v, want one custom Chat message", body["messages"])
	}
	message, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("message = %#v, want object", messages[0])
	}
	content, ok := message["content"].([]any)
	if !ok {
		t.Fatalf("message content = %#v, want parts", message["content"])
	}
	if len(content) != 2 {
		t.Fatalf("content = %#v, want text and video parts", content)
	}
	video, ok := content[1].(map[string]any)
	if !ok || video["type"] != "video_url" {
		t.Fatalf("video part = %#v, want OpenRouter video_url", content[1])
	}
	videoURL, ok := video["video_url"].(map[string]any)
	if !ok || videoURL["url"] != "data:video/mp4;base64,AAAA" {
		t.Fatalf("video_url = %#v, want nested data URL", video["video_url"])
	}
}

func TestOpenCodeSessionHeaders(t *testing.T) {
	h := http.Header{}
	openCodeSessionHeaders(h, nil)
	if got := h.Get(openCodeSessionHeader); got != "" {
		t.Fatalf("nil opts header = %q, want empty", got)
	}
	openCodeSessionHeaders(h, core.ProviderOptions{"prompt_cache_key": "nusashell_cv_abc"})
	if got := h.Get(openCodeSessionHeader); got != "nusashell_cv_abc" {
		t.Fatalf("prompt_cache_key header = %q", got)
	}
	h = http.Header{}
	openCodeSessionHeaders(h, core.ProviderOptions{"session_id": "sess-1"})
	if got := h.Get(openCodeSessionHeader); got != "sess-1" {
		t.Fatalf("session_id header = %q", got)
	}
}

func TestAdapterOpenCodeChatSendsSessionHeader(t *testing.T) {
	var gotSession string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSession = r.Header.Get(openCodeSessionHeader)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"deepseek-v4-flash","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	a := &Adapter{
		ProviderKind: domain.ProviderChat,
		Driver:       domain.ProviderDriverOpenRouter,
		OpenRouter:   false,
		BaseURL:      "https://opencode.ai/zen/go/v1",
		APIKey:       "k",
		Client:       srv.Client(),
	}
	// Point the OpenRouter Chat adapter at the test server while keeping an
	// OpenCode BaseURL on the adapter so requestHeaders attaches
	// x-opencode-session.
	p, err := openrouter.New(openrouter.Config{
		APIKey:         "k",
		BaseURL:        srv.URL + "/v1",
		HTTPClient:     srv.Client(),
		RequestHeaders: a.requestHeaders(),
	})
	if err != nil {
		t.Fatalf("openrouter.New: %v", err)
	}
	_, err = p.Chat(context.Background(), &core.Request{
		Model:           "deepseek-v4-flash",
		Messages:        []core.Message{core.UserText("hi")},
		ProviderOptions: core.ProviderOptions{"prompt_cache_key": "nusashell_cv_opencode"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if gotSession != "nusashell_cv_opencode" {
		t.Fatalf("x-opencode-session = %q, want nusashell_cv_opencode", gotSession)
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

// Gemini speaks the Generative Language wire: model discovery is
// GET {base}/v1beta/models authenticated with x-goog-api-key. Google's API
// root does not serve the OpenAI-shaped /models path, so the gemini kind must
// never fall through to the OpenAI-compatible lister.
func TestListModelsRoutesGemini(t *testing.T) {
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-goog-api-key")
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1beta/models":
			_, _ = w.Write([]byte(`{"models":[
				{"name":"models/gemini-2.5-flash","displayName":"Gemini 2.5 Flash","description":"fast","inputTokenLimit":1048576,"outputTokenLimit":65536,"supportedGenerationMethods":["generateContent","countTokens"]},
				{"name":"models/gemini-embedding-001","displayName":"Embedding","supportedGenerationMethods":["embedContent"]}
			]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ad := &Adapter{
		ProviderKind: domain.ProviderGemini,
		Driver:       domain.ProviderDriverGemini,
		BaseURL:      srv.URL,
		APIKey:       "gemini-key",
		Client:       srv.Client(),
	}
	models, err := ad.ListModels(context.Background(), "gemini-key")
	if err != nil {
		t.Fatalf("gemini ListModels: %v", err)
	}
	if gotPath != "/v1beta/models" {
		t.Fatalf("request path = %q, want /v1beta/models", gotPath)
	}
	if gotKey != "gemini-key" {
		t.Fatalf("x-goog-api-key = %q, want gemini-key", gotKey)
	}
	if len(models) != 1 || models[0].ID != "gemini-2.5-flash" || models[0].Context != 1048576 {
		t.Fatalf("gemini models = %+v", models)
	}
	if models[0].DisplayName != "Gemini 2.5 Flash" || models[0].MaxOutput != 65536 {
		t.Fatalf("gemini model metadata = %+v", models[0])
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
			{"provider_name":"StreamLake","tag":"streamlake","quantization":"unknown","status":0,"latency_last_30m":1.25,"throughput_last_30m":50,"pricing":{"prompt":"0.0000015","completion":"0.0000025"}},
			{"provider_name":"DeepInfra","tag":"deepinfra/fp4","quantization":"fp4","status":-2,"latency_last_30m":{"p50":787,"p75":1292.25,"p90":2382.9,"p99":7492.53},"throughput_last_30m":{"p50":118,"p75":148,"p90":177,"p99":232}},
			{"provider_name":"FreeRoute","tag":"free-route","quantization":"fp16","status":0,"latency_last_30m":null,"throughput_last_30m":null,"pricing":{"prompt":"0","completion":"0"}}
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
	if len(routes) != 3 {
		t.Fatalf("routes = %+v", routes)
	}
	if routes[0].Slug != "streamlake" || routes[0].Name != "StreamLake" || routes[0].Status != 0 {
		t.Fatalf("route[0] = %+v", routes[0])
	}
	if routes[0].Latency == nil || *routes[0].Latency != 1.25 || routes[0].Throughput == nil || *routes[0].Throughput != 50 {
		t.Fatalf("route[0] metrics = %+v", routes[0])
	}
	if routes[0].InputCost == nil || *routes[0].InputCost != 1.5 || routes[0].OutputCost == nil || *routes[0].OutputCost != 2.5 {
		t.Fatalf("route[0] pricing = %+v", routes[0])
	}
	// Percentile object → representative p50 value (latency ms, throughput tok/s).
	if routes[1].Slug != "deepinfra/fp4" || routes[1].Quantization != "fp4" {
		t.Fatalf("route[1] = %+v", routes[1])
	}
	if routes[1].Latency == nil || *routes[1].Latency != 787 || routes[1].Throughput == nil || *routes[1].Throughput != 118 {
		t.Fatalf("route[1] percentile metrics = %+v (latency=%v throughput=%v)", routes[1], routes[1].Latency, routes[1].Throughput)
	}
	if routes[1].InputCost != nil || routes[1].OutputCost != nil {
		t.Fatalf("route[1] missing pricing = %+v, want nil costs", routes[1])
	}
	if routes[2].Latency != nil || routes[2].Throughput != nil {
		t.Fatalf("route[2] null metrics = %+v, want nil", routes[2])
	}
	if routes[2].InputCost == nil || *routes[2].InputCost != 0 || routes[2].OutputCost == nil || *routes[2].OutputCost != 0 {
		t.Fatalf("route[2] zero pricing = %+v, want explicit zero values", routes[2])
	}

	routes, err = ad.ListModelEndpoints(context.Background(), "z-ai/glm-5.2-20260616:free")
	if err != nil {
		t.Fatalf("variant ListModelEndpoints: %v", err)
	}
	if gotPath != "/models/z-ai/glm-5.2-20260616:free/endpoints" && gotPath != "/models/z-ai/glm-5.2-20260616%3Afree/endpoints" {
		t.Fatalf("variant request path = %q", gotPath)
	}
	if len(routes) != 3 {
		t.Fatalf("variant routes = %+v", routes)
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

	// Custom providers use the OpenRouter profile, so endpoint discovery is
	// available when the selected driver/profile is OpenRouter even on a
	// non-openrouter host.
	tagged := &Adapter{
		ProviderKind: domain.ProviderChat,
		Driver:       domain.ProviderDriverOpenRouter,
		OpenRouter:   true,
		BaseURL:      srv.URL,
		Client:       srv.Client(),
	}
	routes, err = tagged.ListModelEndpoints(context.Background(), "x")
	if err != nil {
		t.Fatalf("tagged ListModelEndpoints: %v", err)
	}
	if len(routes) != 3 {
		t.Fatalf("tagged routes = %+v, want custom OpenRouter-profile routes", routes)
	}
}

func TestAdapterListModelEndpointsHTTP4xxReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><title>Not Found | opencode</title></html>`))
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
	routes, err := ad.ListModelEndpoints(context.Background(), "big-pickle")
	if err != nil {
		t.Fatalf("4xx ListModelEndpoints: %v", err)
	}
	if len(routes) != 0 {
		t.Fatalf("4xx routes = %+v, want empty", routes)
	}
}
