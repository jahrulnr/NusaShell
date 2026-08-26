package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nusashell/application"
	"nusashell/contracts"
)

// ---- envelope ----

func TestRPCUnknownMethod(t *testing.T) {
	h := newHarness(t, nil)
	res := h.rpc(t, "no.such.method", map[string]any{})
	if res.OK {
		t.Fatal("unknown method must not succeed")
	}
	if res.Error == nil || res.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("want VALIDATION_ERROR, got %+v", res.Error)
	}
}

func TestRPCPathDerivesMethod(t *testing.T) {
	h := newHarness(t, nil)
	// The method is derived from the URL path (dots → slashes), not the
	// body. A body with a wrong/generic method must still dispatch to the
	// path-derived method. This proves the path is authoritative.
	body := `{"method":"generic.placeholder","payload":{}}`
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/rpc/app/info", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var res contracts.Response
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("rpc failed: %+v", res.Error)
	}
	var info struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(res.Result, &info); err != nil {
		t.Fatal(err)
	}
	if info.Name != "NusaShell" {
		t.Fatalf("name = %q, want NusaShell", info.Name)
	}
}

func TestRPCMalformedBody(t *testing.T) {
	h := newHarness(t, nil)
	resp, err := http.Post(h.server.URL+"/rpc/no/such/method", "application/json", strings.NewReader("{not json"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRPCBodyLimitFitsAttachmentContract(t *testing.T) {
	if maxRPCBodyBytes < 24<<20 {
		t.Fatalf("maxRPCBodyBytes = %d, want at least 24MiB for 4x4MiB attachments", maxRPCBodyBytes)
	}
	h := newHarness(t, nil)
	// A payload larger than the old 1MiB cap must still be parsed as JSON.
	payload := strings.Repeat("a", 3<<20/2)
	body := fmt.Sprintf(`{"method":"no.such.method","payload":{"x":%q}}`, payload)
	resp, err := http.Post(h.server.URL+"/rpc/no/such/method", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (unknown method after a large but valid body)", resp.StatusCode)
	}
	var res contracts.Response
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Error == nil || res.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("want VALIDATION_ERROR for unknown method, got %+v", res)
	}
}

func TestRPCAppInfo(t *testing.T) {
	h := newHarness(t, nil)
	res := h.rpcOK(t, "app.info", map[string]any{})
	var info struct {
		Name     string `json:"name"`
		Version  string `json:"version"`
		DataDir  string `json:"data_dir"`
		Features struct {
			Tools         bool     `json:"tools"`
			MCP           bool     `json:"mcp"`
			Compaction    bool     `json:"compaction"`
			PromptCaching bool     `json:"prompt_caching"`
			Providers     []string `json:"providers"`
		} `json:"features"`
	}
	if err := json.Unmarshal(res.Result, &info); err != nil {
		t.Fatal(err)
	}
	if info.Name != "NusaShell" || info.Version != "test" {
		t.Fatalf("app info = %+v", info)
	}
	if info.Name != "NusaShell" || info.Version != "test" {
		t.Fatalf("app info = %+v", info)
	}
	if !info.Features.Tools || !info.Features.MCP || !info.Features.Compaction || !info.Features.PromptCaching {
		t.Fatalf("features = %+v", info.Features)
	}
}

// TestRoutesNoSSEEndpoint guards the SSE /events takeout: the endpoint is
// gone, so clients that still rely on it fail fast with 404 instead of
// silently hanging on an unknown stream.
func TestRoutesNoSSEEndpoint(t *testing.T) {
	h := newHarness(t, nil)
	resp, err := http.Get(h.server.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /events status = %d, want 404", resp.StatusCode)
	}
}

// ---- conversations ----

func TestConversationLifecycle(t *testing.T) {
	h := newHarness(t, nil)

	// create
	created := h.rpcOK(t, "agent.conversations.create", map[string]any{"title": "Hello"})
	var conv struct {
		Conversation struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			CreatedAt string `json:"created_at"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(created.Result, &conv); err != nil {
		t.Fatal(err)
	}
	id := conv.Conversation.ID
	if id == "" || conv.Conversation.Title != "Hello" || conv.Conversation.CreatedAt == "" {
		t.Fatalf("created conversation = %+v", conv)
	}

	// list
	listed := h.rpcOK(t, "agent.conversations.list", map[string]any{})
	var list struct {
		Conversations []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"conversations"`
	}
	if err := json.Unmarshal(listed.Result, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Conversations) != 1 || list.Conversations[0].ID != id {
		t.Fatalf("list = %+v", list)
	}

	// get
	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": id})
	var get struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(gotten.Result, &get); err != nil {
		t.Fatal(err)
	}
	if len(get.Messages) != 0 {
		t.Fatalf("new conversation has %d messages", len(get.Messages))
	}

	// rename
	renamed := h.rpcOK(t, "agent.conversations.rename", map[string]any{"id": id, "title": "Renamed"})
	var ren struct {
		Conversation struct {
			Title string `json:"title"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(renamed.Result, &ren); err != nil {
		t.Fatal(err)
	}
	if ren.Conversation.Title != "Renamed" {
		t.Fatalf("renamed title = %q", ren.Conversation.Title)
	}

	// delete
	h.rpcOK(t, "agent.conversations.delete", map[string]any{"id": id})
	res := h.rpc(t, "agent.conversations.get", map[string]any{"id": id})
	if res.OK {
		t.Fatal("get after delete must fail")
	}
	if res.Error == nil || res.Error.Code != "NOT_FOUND" {
		t.Fatalf("want NOT_FOUND, got %+v", res.Error)
	}
}

func TestConversationWorkspacePickerPersistsPerConversation(t *testing.T) {
	h := newHarness(t, nil)
	firstID := h.newConversation(t)
	secondID := h.newConversation(t)
	workspace := t.TempDir()
	h.app.WorkspacePicker = application.WorkspacePickerFunc(func(context.Context) (string, error) {
		return workspace, nil
	})

	picked := h.rpcOK(t, "agent.conversations.pick-workspace", map[string]any{"id": firstID})
	var result struct {
		Conversation struct {
			ID        string `json:"id"`
			Workspace string `json:"workspace"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(picked.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Conversation.ID != firstID || result.Conversation.Workspace != workspace {
		t.Fatalf("picked workspace = %+v", result.Conversation)
	}

	other := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": secondID})
	var untouched struct {
		Conversation struct {
			Workspace string `json:"workspace"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(other.Result, &untouched); err != nil {
		t.Fatal(err)
	}
	if untouched.Conversation.Workspace != "" {
		t.Fatalf("workspace leaked into a different conversation: %q", untouched.Conversation.Workspace)
	}
}

func TestTurnPersistsValidatedAttachments(t *testing.T) {
	h := newHarness(t, nil)
	providerID := h.addOpenAIProvider(t, "Attachment provider")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": providerID})
	convID := h.newConversation(t)

	started := h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID,
		"model":           "fake-model-1",
		"text":            "Summarize these attachments",
		"attachments": []map[string]any{
			{"type": "text", "name": "notes.txt", "media_type": "text/plain", "content": "Local notes"},
			{"type": "image", "name": "pixel.png", "media_type": "image/png", "data_url": "data:image/png;base64,iVBORw0KGgo="},
		},
	})
	if providerID == "" || len(started.Result) == 0 {
		t.Fatalf("turn start failed: provider=%q result=%s", providerID, started.Result)
	}
	waitTurnDone(t, h, convID)

	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var result struct {
		Messages []struct {
			Role        string `json:"role"`
			Attachments []struct {
				Type      string `json:"type"`
				Name      string `json:"name"`
				MediaType string `json:"media_type"`
				Content   string `json:"content"`
				DataURL   string `json:"data_url"`
			} `json:"attachments"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotten.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) == 0 || result.Messages[0].Role != "user" || len(result.Messages[0].Attachments) != 2 {
		t.Fatalf("persisted attachments = %+v", result.Messages)
	}
	if result.Messages[0].Attachments[0].Content != "Local notes" || result.Messages[0].Attachments[1].DataURL == "" {
		t.Fatalf("persisted attachment values = %+v", result.Messages[0].Attachments)
	}
}

func TestLogsArePublishedToLiveSubscribers(t *testing.T) {
	h := newHarness(t, nil)
	_, events, unsubscribe := h.app.Bus.Subscribe()
	defer unsubscribe()

	h.rpcOK(t, "agent.conversations.create", map[string]any{"title": "Live log"})

	select {
	case ev := <-events:
		if ev.Type != contracts.EventLogAppend {
			t.Fatalf("event type = %q, want %q", ev.Type, contracts.EventLogAppend)
		}
		var payload contracts.LogAppendEvent
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			t.Fatalf("decode log event: %v", err)
		}
		if payload.Entry.Level != "info" || payload.Entry.Source != "agent" || payload.Entry.Message == "" {
			t.Fatalf("log event payload = %+v", payload.Entry)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for live log event")
	}
}

func TestConversationValidation(t *testing.T) {
	h := newHarness(t, nil)

	res := h.rpc(t, "agent.conversations.get", map[string]any{})
	if res.OK || res.Error == nil || res.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("empty id must be a validation error, got %+v", res)
	}

	res = h.rpc(t, "agent.conversations.get", map[string]any{"id": "missing"})
	if res.OK || res.Error == nil || res.Error.Code != "NOT_FOUND" {
		t.Fatalf("missing conversation must be NOT_FOUND, got %+v", res)
	}

	id := h.newConversation(t)
	res = h.rpc(t, "agent.conversations.rename", map[string]any{"id": id, "title": "  "})
	if res.OK || res.Error == nil || res.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("blank title must be a validation error, got %+v", res)
	}
}

// ---- providers ----

func TestProviderLifecycle(t *testing.T) {
	h := newHarness(t, nil)
	h.llm.models = []string{"m1", "m2"}

	// save
	pid := h.addOpenAIProvider(t, "Fake")
	// import models
	imp := h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	var imported struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(imp.Result, &imported); err != nil {
		t.Fatal(err)
	}
	if len(imported.Models) != 2 {
		t.Fatalf("imported models = %+v", imported.Models)
	}

	// list shows configured + key presence
	listed := h.rpcOK(t, "ai.providers.list", map[string]any{})
	var list struct {
		Providers []struct {
			ID         string `json:"id"`
			Kind       string `json:"kind"`
			Configured bool   `json:"configured"`
			HasAPIKey  bool   `json:"has_api_key"`
			Models     []struct {
				ID string `json:"id"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(listed.Result, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Providers) != 1 {
		t.Fatalf("providers = %+v", list)
	}
	p := list.Providers[0]
	if p.Kind != "chat" || !p.Configured || !p.HasAPIKey || len(p.Models) != 2 {
		t.Fatalf("provider = %+v", p)
	}

	// models list
	models := h.rpcOK(t, "ai.models.list", map[string]any{})
	var ms struct {
		Models []struct {
			ID           string `json:"id"`
			ProviderName string `json:"provider_name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(models.Result, &ms); err != nil {
		t.Fatal(err)
	}
	if len(ms.Models) != 2 || ms.Models[0].ProviderName != "Fake" {
		t.Fatalf("models = %+v", ms)
	}

	// test connection hits the fake provider
	h.rpcOK(t, "ai.providers.test", map[string]any{"id": pid})

	// delete: credential must go too
	h.rpcOK(t, "ai.providers.delete", map[string]any{"id": pid})
	_, has, err := h.creds.Get(pid)
	if err != nil || has {
		t.Fatalf("credential after delete: has=%v err=%v", has, err)
	}
}

func TestProviderValidationAndErrors(t *testing.T) {
	h := newHarness(t, nil)

	res := h.rpc(t, "ai.providers.save", map[string]any{"kind": "chat", "name": ""})
	if res.OK || res.Error == nil || res.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("blank name must fail validation, got %+v", res)
	}

	res = h.rpc(t, "ai.providers.save", map[string]any{"kind": "weird", "name": "X"})
	if res.OK || res.Error == nil || res.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("bad kind must fail validation, got %+v", res)
	}

	// no hidden defaults in the backend: base url and kind are required
	res = h.rpc(t, "ai.providers.save", map[string]any{"kind": "messages", "name": "No URL"})
	if res.OK || res.Error == nil || res.Error.Code != "VALIDATION_ERROR" || !strings.Contains(res.Error.Message, "base url") {
		t.Fatalf("empty base url must fail validation, got %+v", res)
	}

	res = h.rpc(t, "ai.providers.save", map[string]any{"name": "No Kind", "base_url": "http://x/v1"})
	if res.OK || res.Error == nil || res.Error.Code != "VALIDATION_ERROR" || !strings.Contains(res.Error.Message, "kind") {
		t.Fatalf("empty kind must fail validation, got %+v", res)
	}

	res = h.rpc(t, "ai.providers.test", map[string]any{"id": "nope"})
	if res.OK || res.Error == nil || res.Error.Code != "NOT_FOUND" {
		t.Fatalf("missing provider must be NOT_FOUND, got %+v", res)
	}

	// kinds are switchable in place (chat → messages)
	res = h.rpc(t, "ai.providers.save", map[string]any{"kind": "chat", "name": "Switchable", "base_url": "http://x/v1"})
	if !res.OK {
		t.Fatalf("create chat provider must succeed, got %+v", res)
	}
	var switchOut struct {
		Providers []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(res.Result, &switchOut); err != nil || len(switchOut.Providers) == 0 {
		t.Fatalf("chat provider not returned: %s", res.Result)
	}
	switchID := switchOut.Providers[0].ID
	res = h.rpc(t, "ai.providers.save", map[string]any{"id": switchID, "kind": "messages", "name": "Switchable", "base_url": "http://x"})
	if !res.OK || res.Error != nil {
		t.Fatalf("non-codex kind change must succeed, got %+v", res)
	}
	if err := json.Unmarshal(res.Result, &switchOut); err != nil || len(switchOut.Providers) == 0 || switchOut.Providers[0].Kind != "messages" {
		t.Fatalf("kind not updated to messages: %s", res.Result)
	}

	// provider failure surfaces as PROVIDER_ERROR
	pid := h.addOpenAIProvider(t, "Failing")
	h.llm.failStatus = http.StatusInternalServerError
	res = h.rpc(t, "ai.providers.test", map[string]any{"id": pid})
	if res.OK || res.Error == nil || res.Error.Code != "PROVIDER_ERROR" {
		t.Fatalf("provider failure must be PROVIDER_ERROR, got %+v", res)
	}
}

func TestProviderTestProbesModelsEndpoint(t *testing.T) {
	h := newHarness(t, nil)
	h.llm.models = []string{"m1", "m2"}
	// fresh provider, nothing imported yet: the probe must still work
	pid := h.addOpenAIProvider(t, "Fresh")

	res := h.rpcOK(t, "ai.providers.test", map[string]any{"id": pid})
	var out struct {
		OK      bool `json:"ok"`
		Models  int  `json:"models"`
		Latency int  `json:"latency_ms"`
	}
	if err := json.Unmarshal(res.Result, &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.Models != 2 {
		t.Fatalf("probe result = %+v", out)
	}
	if out.Latency < 0 {
		t.Fatalf("latency = %d", out.Latency)
	}
}

func TestProviderTestGatewayErrorPassthrough(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Gateway")
	h.llm.failModelsStatus = http.StatusBadGateway
	// gateway-style error body, as seen on model-routing failures
	h.llm.failBody = `{"error":{"message":"Invalid URL: ","code":502,"metadata":{"provider_name":"Stealth"}},"user_id":"u1"}`

	res := h.rpc(t, "ai.providers.test", map[string]any{"id": pid})
	if res.OK || res.Error == nil || res.Error.Code != "PROVIDER_ERROR" {
		t.Fatalf("gateway 502 must surface as PROVIDER_ERROR, got %+v", res)
	}
	if !strings.Contains(res.Error.Message, "Invalid URL") {
		t.Fatalf("gateway error body should pass through, got %q", res.Error.Message)
	}
	if !strings.Contains(res.Error.Message, "GET /models") {
		t.Fatalf("error should name the probe, got %q", res.Error.Message)
	}
}

func TestProviderTestMessagesKindUsesV1Models(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addAnthropicProvider(t, "Claude")

	res := h.rpcOK(t, "ai.providers.test", map[string]any{"id": pid})
	var out struct {
		OK     bool `json:"ok"`
		Models int  `json:"models"`
	}
	if err := json.Unmarshal(res.Result, &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.Models != 2 {
		t.Fatalf("messages probe result = %+v", out)
	}
	if last := h.llm.lastSeenPath(); !strings.HasSuffix(last, "/v1/models") {
		t.Fatalf("messages probe path = %q, want /v1/models", last)
	}
}

// TestProviderURLsVerbatimVersions pins the base URL joining rules: the
// operation path is appended verbatim to whatever versioned root the user
// configured (v1, v4, …) — no version is ever injected. Messages uses the
// Anthropic-compatible convention (/v1/messages) on bare compat roots.
func TestProviderURLsVerbatimVersions(t *testing.T) {
	h := newHarness(t, nil)

	cases := []struct {
		name      string
		kind      string
		base      string
		wantProbe string
		wantOp    string
	}{
		{
			name: "chat keeps v4 root", kind: "chat",
			base:      h.llm.URL + "/api/paas/v4",
			wantProbe: "/api/paas/v4/models",
			wantOp:    "/api/paas/v4/chat/completions",
		},
		{
			name: "responses keeps v4 root", kind: "responses",
			base:      h.llm.URL + "/api/paas/v4",
			wantProbe: "/api/paas/v4/models",
			wantOp:    "/api/paas/v4/responses",
		},
		{
			name: "messages compat root gets convention suffix", kind: "messages",
			base:      h.llm.URL + "/api/anthropic",
			wantProbe: "/api/anthropic/v1/models",
			wantOp:    "/api/anthropic/v1/messages",
		},
		{
			name: "messages full endpoint used as-is", kind: "messages",
			base:      h.llm.URL + "/api/anthropic/v1/messages",
			wantProbe: "/api/anthropic/v1/messages/v1/models", // probe on a pasted endpoint is best-effort
			wantOp:    "/api/anthropic/v1/messages",
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// a unique model per subtest so the turn resolves to this
			// provider instead of an earlier one sharing default ids
			model := fmt.Sprintf("model-%d", i)
			h.llm.models = []string{model}

			res := h.rpcOK(t, "ai.providers.save", map[string]any{
				"kind": tc.kind, "name": "V", "base_url": tc.base, "api_key": "k", "enabled": true,
			})
			var out struct {
				Providers []struct {
					ID string `json:"id"`
				} `json:"providers"`
			}
			if err := json.Unmarshal(res.Result, &out); err != nil || len(out.Providers) == 0 {
				t.Fatalf("save: %s", res.Result)
			}
			pid := out.Providers[0].ID

			// probe: GET /models
			h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
			if got := h.llm.lastSeenPath(); got != tc.wantProbe {
				t.Fatalf("probe path = %q, want %q", got, tc.wantProbe)
			}

			// actual operation: one streaming turn
			convID := h.newConversation(t)
			h.llm.setRounds([][]llmStep{{{Text: "ok"}}})
			h.rpcOK(t, "agent.turns.start", map[string]any{
				"conversation_id": convID, "text": "hi", "model": model,
			})
			waitTurnDone(t, h, convID)
			if got := h.llm.lastSeenPath(); got != tc.wantOp {
				t.Fatalf("operation path = %q, want %q", got, tc.wantOp)
			}
		})
	}
}

// ---- skills ----

func TestSkillLifecycle(t *testing.T) {
	h := newHarness(t, nil)

	res := h.rpc(t, "skills.save", map[string]any{"name": "", "content": "x"})
	if res.OK || res.Error == nil || res.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("blank skill name must fail, got %+v", res)
	}
	res = h.rpc(t, "skills.save", map[string]any{"name": "x", "content": "  "})
	if res.OK || res.Error == nil || res.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("blank skill content must fail, got %+v", res)
	}

	saved := h.rpcOK(t, "skills.save", map[string]any{
		"name": "code-review", "description": "Review code changes", "content": "# Review\nCheck for bugs.",
	})
	var skill struct {
		Skill struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"skill"`
	}
	if err := json.Unmarshal(saved.Result, &skill); err != nil {
		t.Fatal(err)
	}
	sid := skill.Skill.ID

	read := h.rpcOK(t, "skills.read", map[string]any{"id": sid})
	var full struct {
		Skill struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Content     string `json:"content"`
		} `json:"skill"`
	}
	if err := json.Unmarshal(read.Result, &full); err != nil {
		t.Fatal(err)
	}
	if full.Skill.Content != "# Review\nCheck for bugs." || full.Skill.Description != "Review code changes" {
		t.Fatalf("skill = %+v", full)
	}

	// update preserves id
	updated := h.rpcOK(t, "skills.save", map[string]any{"id": sid, "name": "code-review", "content": "v2"})
	var upd struct {
		Skill struct {
			ID string `json:"id"`
		} `json:"skill"`
	}
	_ = json.Unmarshal(updated.Result, &upd)
	if upd.Skill.ID != sid {
		t.Fatalf("update changed id: %s != %s", upd.Skill.ID, sid)
	}

	h.rpcOK(t, "skills.delete", map[string]any{"id": sid})
	res = h.rpc(t, "skills.read", map[string]any{"id": sid})
	if res.OK || res.Error == nil || res.Error.Code != "NOT_FOUND" {
		t.Fatalf("read after delete must be NOT_FOUND, got %+v", res)
	}
}

// ---- memory ----

func TestMemoryHandlers(t *testing.T) {
	h := newHarness(t, nil)

	h.rpcOK(t, "memory.save", map[string]any{"content": "user prefers dark mode", "tags": []string{"prefs"}})
	h.rpcOK(t, "memory.save", map[string]any{"content": "project is Go"})

	search := h.rpcOK(t, "memory.search", map[string]any{"query": "dark mode"})
	var out struct {
		Entries []struct {
			ID      string   `json:"id"`
			Content string   `json:"content"`
			Tags    []string `json:"tags"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(search.Result, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 1 || out.Entries[0].Content != "user prefers dark mode" || len(out.Entries[0].Tags) != 1 {
		t.Fatalf("search = %+v", out)
	}

	res := h.rpc(t, "memory.save", map[string]any{"content": "   "})
	if res.OK || res.Error == nil || res.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("blank memory must fail, got %+v", res)
	}

	h.rpcOK(t, "memory.delete", map[string]any{"id": out.Entries[0].ID})
	res = h.rpc(t, "memory.delete", map[string]any{"id": "nope"})
	if res.OK || res.Error == nil || res.Error.Code != "NOT_FOUND" {
		t.Fatalf("delete missing memory must be NOT_FOUND, got %+v", res)
	}
}

// ---- docs ----

func TestDocsHandlers(t *testing.T) {
	h := newHarness(t, nil)

	listed := h.rpcOK(t, "docs.list", map[string]any{})
	var list struct {
		Docs []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Path  string `json:"path"`
		} `json:"docs"`
	}
	if err := json.Unmarshal(listed.Result, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Docs) < 6 {
		t.Fatalf("docs corpus too small: %d", len(list.Docs))
	}

	searched := h.rpcOK(t, "docs.search", map[string]any{"query": "mcp"})
	var hits struct {
		Results []struct {
			ID      string `json:"id"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}
	if err := json.Unmarshal(searched.Result, &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits.Results) == 0 {
		t.Fatal("docs.search mcp returned nothing")
	}
	found := false
	for _, r := range hits.Results {
		if r.ID == "mcp" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mcp doc not among hits: %+v", hits.Results)
	}

	read := h.rpcOK(t, "docs.read", map[string]any{"id": "mcp"})
	var doc struct {
		Doc struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		} `json:"doc"`
	}
	if err := json.Unmarshal(read.Result, &doc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Doc.Content, "stdio") {
		t.Fatalf("mcp doc content missing stdio: %q", doc.Doc.Content[:80])
	}

	res := h.rpc(t, "docs.read", map[string]any{"id": "nope"})
	if res.OK || res.Error == nil || res.Error.Code != "NOT_FOUND" {
		t.Fatalf("missing doc must be NOT_FOUND, got %+v", res)
	}
}

// ---- logs / settings ----

func TestLogsHandlers(t *testing.T) {
	h := newHarness(t, nil)
	// generate some activity
	h.newConversation(t)

	listed := h.rpcOK(t, "logs.list", map[string]any{"limit": 50})
	var out struct {
		Entries []struct {
			ID      string `json:"id"`
			Level   string `json:"level"`
			Source  string `json:"source"`
			Message string `json:"message"`
			Time    string `json:"time"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(listed.Result, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) == 0 {
		t.Fatal("logs.list empty after activity")
	}
	for _, e := range out.Entries {
		if e.ID == "" || e.Time == "" || e.Level == "" || e.Source == "" {
			t.Fatalf("malformed log entry: %+v", e)
		}
	}

	filtered := h.rpcOK(t, "logs.list", map[string]any{"level": "error"})
	var errs struct {
		Entries []struct {
			Level string `json:"level"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(filtered.Result, &errs); err != nil {
		t.Fatal(err)
	}
	for _, e := range errs.Entries {
		if e.Level != "error" {
			t.Fatalf("level filter leaked %q", e.Level)
		}
	}

	h.rpcOK(t, "logs.clear", map[string]any{})
	cleared := h.rpcOK(t, "logs.list", map[string]any{})
	var empty struct {
		Entries []json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(cleared.Result, &empty); err != nil || len(empty.Entries) != 0 {
		t.Fatalf("logs after clear = %s", cleared.Result)
	}
}

func TestSettingsHandlers(t *testing.T) {
	h := newHarness(t, nil)

	gotten := h.rpcOK(t, "settings.get", map[string]any{})
	var out struct {
		Settings struct {
			CompactionEnabled   bool `json:"compaction_enabled"`
			CompactionThreshold int  `json:"compaction_threshold"`
			PromptCaching       bool `json:"prompt_caching"`
			MaxToolRounds       int  `json:"max_tool_rounds"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(gotten.Result, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Settings.CompactionEnabled || out.Settings.CompactionThreshold != 0 || !out.Settings.PromptCaching || out.Settings.MaxToolRounds != 8 {
		t.Fatalf("defaults = %+v", out.Settings)
	}

	threshold := 5000
	promptCaching := false
	maxToolRounds := 24
	settled := h.rpcOK(t, "settings.set", map[string]any{
		"compaction_threshold": threshold,
		"prompt_caching":       promptCaching,
		"max_tool_rounds":      maxToolRounds,
	})
	if err := json.Unmarshal(settled.Result, &out); err != nil {
		t.Fatal(err)
	}
	if out.Settings.CompactionThreshold != 5000 || out.Settings.PromptCaching || out.Settings.MaxToolRounds != maxToolRounds {
		t.Fatalf("after set = %+v", out.Settings)
	}

	// 0 is valid (auto = 80% of model context window).
	h.rpcOK(t, "settings.set", map[string]any{"compaction_threshold": 0})

	res := h.rpc(t, "settings.set", map[string]any{"compaction_threshold": -1})
	if res.OK || res.Error == nil || res.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("negative threshold must fail validation, got %+v", res)
	}
	res = h.rpc(t, "settings.set", map[string]any{"max_tool_rounds": 0})
	if res.OK || res.Error == nil || res.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("zero max tool rounds must fail validation, got %+v", res)
	}
}

// ---- MCP servers stop / plugin uninstall ----

func TestMCPServersStopDropsConnection(t *testing.T) {
	h := newHarness(t, nil)

	// Register a stdio MCP server backed by the fakemcp binary.
	saved := h.rpcOK(t, "plugin.save", map[string]any{
		"name": "fakemcp", "command": h.mcpBin, "args": []string{}, "enabled": true,
	})
	var savedOut struct {
		Servers []struct {
			ID string `json:"id"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(saved.Result, &savedOut); err != nil {
		t.Fatal(err)
	}
	serverID := savedOut.Servers[0].ID

	// Connect so the toolbox caches the connection.
	tested := h.rpcOK(t, "plugin.test", map[string]any{"id": serverID})
	var testOut struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(tested.Result, &testOut); err != nil {
		t.Fatal(err)
	}
	if len(testOut.Tools) == 0 {
		t.Fatalf("mcp test returned no tools: %s", tested.Result)
	}

	// List must report the server as connected.
	listed := h.rpcOK(t, "plugin.list", map[string]any{})
	var listOut struct {
		Servers []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(listed.Result, &listOut); err != nil {
		t.Fatal(err)
	}
	var before string
	for _, s := range listOut.Servers {
		if s.ID == serverID {
			before = s.Status
		}
	}
	if before != "connected" {
		t.Fatalf("status before stop = %q, want connected", before)
	}

	// Stop drops the cached connection.
	h.rpcOK(t, "plugin.stop", map[string]any{"id": serverID})

	listed2 := h.rpcOK(t, "plugin.list", map[string]any{})
	var listOut2 struct {
		Servers []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(listed2.Result, &listOut2); err != nil {
		t.Fatal(err)
	}
	var after string
	for _, s := range listOut2.Servers {
		if s.ID == serverID {
			after = s.Status
		}
	}
	if after == "connected" {
		t.Fatalf("status after stop = connected, want idle/error")
	}
}

func TestPluginUninstallRemovesPluginAndDropsMCP(t *testing.T) {
	h := newHarness(t, nil)

	// Install a fake plugin into the harness plugin store. The plugin
	// points at the fakemcp binary so plugin.test can connect.
	src := t.TempDir()
	manifest := map[string]any{
		"id":      "test.echo-plugin",
		"name":    "Echo Plugin",
		"version": "0.1.0",
		"icon":    "🔊",
		"mcp": map[string]any{
			"transport": "stdio",
			"command":   h.mcpBin,
			"args":      []string{},
		},
	}
	manifestBytes, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(src, "manifest.json"), manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	installed, err := h.plugins.Install(src)
	if err != nil {
		t.Fatalf("install plugin: %v", err)
	}
	pluginID := installed.Manifest.ID

	// The plugin must appear in plugin.list with its manifest metadata.
	listed := h.rpcOK(t, "plugin.list", map[string]any{})
	var listOut struct {
		Plugins []struct {
			ID      string `json:"id"`
			Version string `json:"version"`
			Icon    string `json:"icon"`
			Status  string `json:"status"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(listed.Result, &listOut); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, p := range listOut.Plugins {
		if p.ID == pluginID {
			found = true
			if p.Version != "0.1.0" {
				t.Fatalf("plugin version = %q, want 0.1.0", p.Version)
			}
			if p.Icon != "🔊" {
				t.Fatalf("plugin icon = %q, want 🔊", p.Icon)
			}
			if p.Status != "idle" {
				t.Fatalf("plugin status = %q, want idle (not connected)", p.Status)
			}
		}
	}
	if !found {
		t.Fatalf("plugin %s not in plugin.list: %s", pluginID, listed.Result)
	}

	// Uninstall via RPC.
	h.rpcOK(t, "plugin.uninstall", map[string]any{"id": pluginID})

	// The plugin must no longer appear in plugin.list.
	listed2 := h.rpcOK(t, "plugin.list", map[string]any{})
	var listOut2 struct {
		Servers []struct {
			ID string `json:"id"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(listed2.Result, &listOut2); err != nil {
		t.Fatal(err)
	}
	for _, s := range listOut2.Servers {
		if s.ID == "plugin:"+pluginID {
			t.Fatalf("plugin %s still listed after uninstall", pluginID)
		}
	}

	// Uninstalling again must fail with NOT_FOUND.
	res := h.rpc(t, "plugin.uninstall", map[string]any{"id": pluginID})
	if res.OK || res.Error == nil || res.Error.Code != "NOT_FOUND" {
		t.Fatalf("second uninstall want NOT_FOUND, got %+v", res)
	}
}

func TestMCPServersListReportsPluginUI(t *testing.T) {
	h := newHarness(t, nil)
	src := t.TempDir()
	manifest := map[string]any{
		"id":      "test.ui-plugin",
		"name":    "UI Plugin",
		"version": "0.1.0",
		"icon":    "🧩",
		"ui":      map[string]any{"entry": "ui/index.html"},
		"mcp": map[string]any{
			"transport": "stdio",
			"command":   h.mcpBin,
		},
	}
	manifestBytes, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(src, "manifest.json"), manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	installed, err := h.plugins.Install(src)
	if err != nil {
		t.Fatalf("install plugin: %v", err)
	}

	listed := h.rpcOK(t, "plugin.list", map[string]any{})
	var result struct {
		Plugins []struct {
			ID    string `json:"id"`
			HasUI bool   `json:"hasUI"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(listed.Result, &result); err != nil {
		t.Fatal(err)
	}
	for _, p := range result.Plugins {
		if p.ID == installed.Manifest.ID {
			if !p.HasUI {
				t.Fatalf("plugin metadata = %+v, want hasUI", p)
			}
			return
		}
	}
	t.Fatalf("UI plugin %q not found in plugin.list: %s", installed.Manifest.ID, listed.Result)
}

// helper: POST raw bytes (used by malformed body test)
var _ = bytes.NewBuffer
