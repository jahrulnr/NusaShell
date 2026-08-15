package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"nusashell/application"
	"nusashell/domain"
)

func TestBuildCodexRequestInjectsDefaultInstructions(t *testing.T) {
	req := application.ChatRequest{
		Model:    "gpt-5.3-codex",
		Messages: []application.ChatMessage{{Role: "user", Content: "hello"}},
	}
	out := buildCodexRequest(req)
	if out.Instructions != DefaultInstructions {
		t.Fatalf("instructions = %q, want default", out.Instructions)
	}
	if !out.Stream {
		t.Fatal("stream must be true (Codex requires it)")
	}
	if out.Store {
		t.Fatal("store must be false (Codex requirement)")
	}
}

func TestBuildCodexRequestKeepsCallerInstructions(t *testing.T) {
	req := application.ChatRequest{
		Model:    "gpt-5.3-codex",
		System:   "You are a test assistant.",
		Messages: []application.ChatMessage{{Role: "user", Content: "hello"}},
	}
	out := buildCodexRequest(req)
	if out.Instructions != "You are a test assistant." {
		t.Fatalf("instructions = %q, want caller system", out.Instructions)
	}
}

func TestBuildCodexRequestConvertsSystemToDeveloper(t *testing.T) {
	req := application.ChatRequest{
		Model: "gpt-5.3-codex",
		Messages: []application.ChatMessage{
			{Role: "system", Content: "System prompt"},
			{Role: "user", Content: "hello"},
		},
	}
	out := buildCodexRequest(req)
	if len(out.Input) < 2 {
		t.Fatalf("expected 2 input items, got %d", len(out.Input))
	}
	if out.Input[0].Role != "developer" {
		t.Fatalf("system role = %q, want developer", out.Input[0].Role)
	}
}

func TestBuildCodexRequestDefaultReasoningLow(t *testing.T) {
	req := application.ChatRequest{
		Model:    "gpt-5.3-codex",
		Messages: []application.ChatMessage{{Role: "user", Content: "hello"}},
	}
	out := buildCodexRequest(req)
	if out.Reasoning == nil {
		t.Fatal("reasoning must not be nil")
	}
	if out.Reasoning.Effort != "low" {
		t.Fatalf("default effort = %q, want low", out.Reasoning.Effort)
	}
	if out.Reasoning.Summary != "auto" {
		t.Fatalf("summary = %q, want auto", out.Reasoning.Summary)
	}
	if len(out.Include) == 0 || out.Include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %v, want [reasoning.encrypted_content]", out.Include)
	}
}

func TestBuildCodexRequestExplicitEffort(t *testing.T) {
	req := application.ChatRequest{
		Model:    "gpt-5.3-codex",
		Effort:   "high",
		Messages: []application.ChatMessage{{Role: "user", Content: "hello"}},
	}
	out := buildCodexRequest(req)
	if out.Reasoning.Effort != "high" {
		t.Fatalf("effort = %q, want high", out.Reasoning.Effort)
	}
}

func TestBuildCodexRequestNoneEffortSkipsInclude(t *testing.T) {
	req := application.ChatRequest{
		Model:    "gpt-5.3-codex",
		Effort:   "none",
		Messages: []application.ChatMessage{{Role: "user", Content: "hello"}},
	}
	out := buildCodexRequest(req)
	if out.Reasoning.Effort != "none" {
		t.Fatalf("effort = %q, want none", out.Reasoning.Effort)
	}
	if len(out.Include) != 0 {
		t.Fatalf("include = %v, want empty for effort=none", out.Include)
	}
}

func TestBuildCodexRequestPromptCacheKeyOnly(t *testing.T) {
	req := application.ChatRequest{
		Model:    "gpt-5.3-codex",
		Messages: []application.ChatMessage{{Role: "user", Content: "hello"}},
		PromptCache: &application.PromptCachePolicy{
			Mode: "auto",
			Key:  "pc_abc123",
		},
	}
	out := buildCodexRequest(req)
	if out.PromptCacheKey != "pc_abc123" {
		t.Fatalf("prompt_cache_key = %q, want pc_abc123", out.PromptCacheKey)
	}
	// Verify the request does NOT carry prompt_cache_options or
	// prompt_cache_retention — Codex doesn't support them.
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "prompt_cache_options") {
		t.Fatalf("request must not contain prompt_cache_options: %s", b)
	}
	if strings.Contains(string(b), "prompt_cache_retention") {
		t.Fatalf("request must not contain prompt_cache_retention: %s", b)
	}
}

func TestBuildCodexRequestPromptCacheOffSkipsKey(t *testing.T) {
	req := application.ChatRequest{
		Model:    "gpt-5.3-codex",
		Messages: []application.ChatMessage{{Role: "user", Content: "hello"}},
		PromptCache: &application.PromptCachePolicy{
			Mode: "off",
			Key:  "pc_abc123",
		},
	}
	out := buildCodexRequest(req)
	if out.PromptCacheKey != "" {
		t.Fatalf("prompt_cache_key = %q, want empty for mode=off", out.PromptCacheKey)
	}
}

func TestBuildCodexRequestEmptyInputGetsPlaceholder(t *testing.T) {
	req := application.ChatRequest{
		Model:    "gpt-5.3-codex",
		Messages: nil,
	}
	out := buildCodexRequest(req)
	if len(out.Input) == 0 {
		t.Fatal("input must not be empty (Codex rejects empty input)")
	}
}

func TestBuildCodexRequestStripsUnsupportedFields(t *testing.T) {
	req := application.ChatRequest{
		Model:            "gpt-5.3-codex",
		Messages:         []application.ChatMessage{{Role: "user", Content: "hello"}},
		Temperature:      floatPtr(0.7),
		TopP:             floatPtr(0.9),
		FrequencyPenalty: floatPtr(0.5),
		PresencePenalty:  floatPtr(0.3),
		MaxTokens:        4096,
	}
	out := buildCodexRequest(req)
	b, _ := json.Marshal(out)
	// Codex rejects temperature, top_p, frequency_penalty, presence_penalty,
	// max_output_tokens. Our codexRequest struct doesn't even have these
	// fields, so they can never leak.
	for _, field := range []string{"temperature", "top_p", "frequency_penalty", "presence_penalty", "max_output_tokens"} {
		if strings.Contains(string(b), field) {
			t.Fatalf("request must not contain %s: %s", field, b)
		}
	}
}

func TestHeadersIncludeOriginatorAndAccountID(t *testing.T) {
	a := &Adapter{AccessToken: "tok123", AccountID: "acc456"}
	h := a.headers()
	if h["Authorization"] != "Bearer tok123" {
		t.Fatalf("Authorization = %q, want Bearer tok123", h["Authorization"])
	}
	if h["originator"] != DefaultOriginator {
		t.Fatalf("originator = %q, want %q", h["originator"], DefaultOriginator)
	}
	if h["ChatGPT-Account-ID"] != "acc456" {
		t.Fatalf("ChatGPT-Account-ID = %q, want acc456", h["ChatGPT-Account-ID"])
	}
}

func TestHeadersWithoutAccountID(t *testing.T) {
	a := &Adapter{AccessToken: "tok123"}
	h := a.headers()
	if _, ok := h["ChatGPT-Account-ID"]; ok {
		t.Fatal("ChatGPT-Account-ID must be absent when AccountID is empty")
	}
}

func TestAdapterKind(t *testing.T) {
	a := &Adapter{}
	if a.Kind() != domain.ProviderCodex {
		t.Fatalf("Kind = %q, want codex", a.Kind())
	}
}

func TestAdapterDefaultBaseURL(t *testing.T) {
	a := &Adapter{}
	if a.baseURL() != DefaultBaseURL {
		t.Fatalf("baseURL = %q, want %q", a.baseURL(), DefaultBaseURL)
	}
}

func TestAdapterCustomBaseURL(t *testing.T) {
	a := &Adapter{BaseURL: "https://custom.example.com/codex"}
	if a.baseURL() != "https://custom.example.com/codex" {
		t.Fatalf("baseURL = %q, want custom", a.baseURL())
	}
}

func TestStreamParsesResponseCompletedUsage(t *testing.T) {
	// Simulate a Codex SSE stream with usage in response.completed.
	var sb strings.Builder
	sb.WriteString("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n\n")
	sb.WriteString("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":50,\"input_tokens_details\":{\"cached_tokens\":40,\"cache_write_tokens\":10}}}}\n\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte(sb.String()))
	}))
	defer server.Close()

	a := &Adapter{
		BaseURL:     server.URL,
		AccessToken: "test-token",
		Client:      server.Client(),
	}

	var deltas []string
	resp, err := a.Stream(context.Background(), application.ChatRequest{
		Model:    "gpt-5.3-codex",
		Messages: []application.ChatMessage{{Role: "user", Content: "hi"}},
	}, func(s string) { deltas = append(deltas, s) }, nil)
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	if resp.Content != "Hello" {
		t.Fatalf("Content = %q, want Hello", resp.Content)
	}
	if len(deltas) != 1 || deltas[0] != "Hello" {
		t.Fatalf("deltas = %v, want [Hello]", deltas)
	}
	if resp.Usage.InputTokens != 100 || resp.Usage.OutputTokens != 50 {
		t.Fatalf("Usage = %+v, want input=100 output=50", resp.Usage)
	}
	if resp.Usage.CacheRead != 40 {
		t.Fatalf("CacheRead = %d, want 40", resp.Usage.CacheRead)
	}
	if resp.Usage.CacheWrite != 10 {
		t.Fatalf("CacheWrite = %d, want 10", resp.Usage.CacheWrite)
	}
}

func TestStreamParsesFunctionCall(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"call_id\":\"call_123\",\"name\":\"get_weather\"}}\n\n")
	sb.WriteString("event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"delta\":\"{\\\"city\\\":\\\"SF\\\"}\"}\n\n")
	sb.WriteString("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":5,\"input_tokens_details\":{\"cached_tokens\":0,\"cache_write_tokens\":0}}}}\n\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte(sb.String()))
	}))
	defer server.Close()

	a := &Adapter{
		BaseURL:     server.URL,
		AccessToken: "test-token",
		Client:      server.Client(),
	}

	resp, err := a.Stream(context.Background(), application.ChatRequest{
		Model:    "gpt-5.3-codex",
		Messages: []application.ChatMessage{{Role: "user", Content: "weather?"}},
	}, nil, nil)
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %v, want 1", resp.ToolCalls)
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_123" || tc.Name != "get_weather" {
		t.Fatalf("ToolCall = %+v, want call_123/get_weather", tc)
	}
	if tc.Args != `{"city":"SF"}` {
		t.Fatalf("Args = %q, want {\"city\":\"SF\"}", tc.Args)
	}
}

func TestStreamHandlesResponseFailed(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"rate limited\"}}}\n\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte(sb.String()))
	}))
	defer server.Close()

	a := &Adapter{
		BaseURL:     server.URL,
		AccessToken: "test-token",
		Client:      server.Client(),
	}

	_, err := a.Stream(context.Background(), application.ChatRequest{
		Model:    "gpt-5.3-codex",
		Messages: []application.ChatMessage{{Role: "user", Content: "hi"}},
	}, nil, nil)
	if err == nil {
		t.Fatal("expected error for response.failed")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("error = %v, want 'rate limited'", err)
	}
}

func TestExtractAccountIDFromJWT(t *testing.T) {
	// Build a fake JWT with the auth claim.
	payload, _ := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acc_test_123",
		},
	})
	encoded := base64RawURL(payload)
	token := "header." + encoded + ".signature"

	got := extractAccountID(token)
	if got != "acc_test_123" {
		t.Fatalf("extractAccountID = %q, want acc_test_123", got)
	}
}

func TestExtractAccountIDInvalidJWT(t *testing.T) {
	if got := extractAccountID("not-a-jwt"); got != "" {
		t.Fatalf("extractAccountID = %q, want empty", got)
	}
	if got := extractAccountID("a.b"); got != "" {
		t.Fatalf("extractAccountID = %q, want empty for 2-part", got)
	}
}

func TestTokenJSONIsExpired(t *testing.T) {
	tok := &TokenJSON{ExpiresAt: time.Now().Add(-time.Hour).Unix()}
	if !tok.IsExpired(0) {
		t.Fatal("expired token should report IsExpired=true")
	}
	tok2 := &TokenJSON{ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if tok2.IsExpired(5 * time.Minute) {
		t.Fatal("token with 1h left should not be expired with 5m margin")
	}
	if !tok2.IsExpired(2 * time.Hour) {
		t.Fatal("token with 1h left should be expired with 2h margin")
	}
	// ExpiresAt=0 with RefreshToken: treat as expired so first use refreshes.
	tok3 := &TokenJSON{ExpiresAt: 0, RefreshToken: "refresh"}
	if !tok3.IsExpired(0) {
		t.Fatal("unknown expiry with refresh token should be expired (refresh on first use)")
	}
	// ExpiresAt=0 without RefreshToken: can't refresh, assume valid.
	tok4 := &TokenJSON{ExpiresAt: 0}
	if tok4.IsExpired(0) {
		t.Fatal("unknown expiry without refresh token should not be expired (can't refresh)")
	}
}

func TestTokenJSONMarshalUnmarshal(t *testing.T) {
	original := &TokenJSON{
		AccessToken:  "access123",
		RefreshToken: "refresh456",
		AccountID:    "acc789",
		ExpiresAt:    1700000000,
	}
	s, err := original.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := UnmarshalToken(s)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.AccessToken != original.AccessToken || parsed.RefreshToken != original.RefreshToken || parsed.AccountID != original.AccountID || parsed.ExpiresAt != original.ExpiresAt {
		t.Fatalf("round-trip mismatch: %+v vs %+v", parsed, original)
	}
}

// --- helpers ---

func floatPtr(v float64) *float64 { return &v }

func base64RawURL(b []byte) string {
	return base64RawURLEncode(b)
}

// use encoding/base64 directly to avoid import cycle in test helpers
func base64RawURLEncode(b []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	var sb strings.Builder
	for i := 0; i < len(b); i += 3 {
		sb.WriteByte(alphabet[b[i]>>2])
		v := (b[i] & 0x3) << 4
		if i+1 < len(b) {
			v |= b[i+1] >> 4
			sb.WriteByte(alphabet[v])
			v = (b[i+1] & 0xf) << 2
			if i+2 < len(b) {
				v |= b[i+2] >> 6
				sb.WriteByte(alphabet[v])
				sb.WriteByte(alphabet[b[i+2]&0x3f])
			} else {
				sb.WriteByte(alphabet[v])
			}
		} else {
			sb.WriteByte(alphabet[v])
		}
	}
	return sb.String()
}

// ---- CompactServer tests ----
//
// CompactServer now uses a Codex app-server subprocess (JSON-RPC) instead
// of direct HTTP. The subprocess tests use a fake binary to avoid requiring
// a real Codex CLI installation.

func TestCompactServerEmptyConversation(t *testing.T) {
	a := &Adapter{}
	summary, err := a.CompactServer(context.Background(), &domain.Conversation{}, "gpt-5.6-luna", 128000)
	if err != nil {
		t.Fatalf("expected nil error for empty conversation, got: %v", err)
	}
	if summary != "" {
		t.Fatalf("summary = %q, want empty for empty conversation", summary)
	}
}

func TestCompactServerSingleMessage(t *testing.T) {
	a := &Adapter{}
	c := &domain.Conversation{
		ID: "conv1",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "hello"},
		},
	}
	summary, err := a.CompactServer(context.Background(), c, "gpt-5.6-luna", 128000)
	if err != nil {
		t.Fatalf("expected nil error for single message, got: %v", err)
	}
	if summary != "" {
		t.Fatalf("summary = %q, want empty for single message", summary)
	}
}

func TestCompactServerSubprocessSuccess(t *testing.T) {
	binPath := buildFakeCodex(t)

	oldBin := CodexBinary
	CodexBinary = binPath
	oldSkip := skipRuntimeManager
	skipRuntimeManager = true
	defer func() {
		CodexBinary = oldBin
		skipRuntimeManager = oldSkip
	}()

	a := &Adapter{}
	c := &domain.Conversation{
		ID: "conv1",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "hello"},
			{Role: domain.RoleAssistant, Content: "hi there"},
			{Role: domain.RoleUser, Content: "tell me about testing"},
		},
	}
	summary, err := a.CompactServer(context.Background(), c, "gpt-5.6-luna", 128000)
	if err != nil {
		t.Fatalf("CompactServer failed: %v", err)
	}
	if !strings.Contains(summary, "testing") {
		t.Fatalf("summary = %q, want to contain 'testing'", summary)
	}
}

func TestCompactServerSubprocessBinaryNotFound(t *testing.T) {
	oldBin := CodexBinary
	CodexBinary = "/nonexistent/path/codex-binary-12345"
	oldSkip := skipRuntimeManager
	skipRuntimeManager = true
	defer func() {
		CodexBinary = oldBin
		skipRuntimeManager = oldSkip
	}()

	a := &Adapter{}
	c := &domain.Conversation{
		ID: "conv1",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "hello"},
			{Role: domain.RoleAssistant, Content: "hi"},
		},
	}
	_, err := a.CompactServer(context.Background(), c, "gpt-5.6-luna", 128000)
	if err == nil {
		t.Fatal("expected error when binary not found")
	}
}

func TestBuildTurnInputUserOnly(t *testing.T) {
	c := &domain.Conversation{
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "hello"},
			{Role: domain.RoleAssistant, Content: "hi"},
			{Role: domain.RoleUser, Content: "how are you?"},
		},
	}
	input := buildTurnInput(c)
	// Only user messages should be included
	if len(input) != 2 {
		t.Fatalf("expected 2 items (user only), got %d", len(input))
	}
	for i, item := range input {
		if item["type"] != "text" {
			t.Fatalf("item[%d] type = %v, want text", i, item["type"])
		}
	}
}

func TestBuildTurnInputSkipsSystemMessages(t *testing.T) {
	c := &domain.Conversation{
		Messages: []domain.Message{
			{Role: domain.RoleSystem, Content: "compaction handoff..."},
			{Role: domain.RoleUser, Content: "hello"},
		},
	}
	input := buildTurnInput(c)
	if len(input) != 1 {
		t.Fatalf("expected 1 item (system skipped), got %d", len(input))
	}
	if input[0]["type"] != "text" {
		t.Fatalf("first item type = %v, want text", input[0]["type"])
	}
}

func TestBuildTurnInputEmptyContent(t *testing.T) {
	c := &domain.Conversation{
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: ""},
			{Role: domain.RoleUser, Content: "hello"},
		},
	}
	input := buildTurnInput(c)
	if len(input) != 1 {
		t.Fatalf("expected 1 item (empty content skipped), got %d", len(input))
	}
}

func buildFakeCodex(t *testing.T) string {
	t.Helper()
	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("module root: %v", err)
	}
	out := filepath.Join(t.TempDir(), "fake-codex")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, filepath.Join(root, "testdata", "fakecodex"))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fakecodex: %v\n%s", err, b)
	}
	return out
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}
