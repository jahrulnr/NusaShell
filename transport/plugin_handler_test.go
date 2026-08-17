package transport

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"nusashell/domain"
	"nusashell/infrastructure/mcpclient"
	"nusashell/infrastructure/pluginfs"
	"nusashell/infrastructure/pluginruntime"
)

// TestPluginHandlerCallToolForwardsStructuredContent verifies that the
// plugin UI bridge forwards the full MCP CallToolResult (content +
// structuredContent + isError) to the UI. Plugin UIs expect
// structuredContent so they can render JSON without parsing
// human-readable text. Dropping structuredContent (the old behavior)
// breaks plugins like files/terminal that return a text summary plus a
// structured payload.
func TestPluginHandlerCallToolForwardsStructuredContent(t *testing.T) {
	if fakemcpBin == "" {
		t.Fatal("fakemcpBin not built")
	}
	storeDir := t.TempDir()
	store, err := pluginfs.New(filepath.Join(storeDir, "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	// Register a plugin whose MCP server is the fakemcp test binary.
	plugin := &domain.Plugin{
		Manifest: domain.PluginManifest{
			ID:      "test.fakemcp",
			Name:    "Fake MCP",
			Version: "1.0.0",
			Icon:    "test",
			MCP: domain.PluginMCPConfig{
				Transport: domain.PluginTransportStdio,
				Command:   fakemcpBin,
			},
		},
		InstallPath: filepath.Dir(fakemcpBin),
	}
	if err := store.Save(plugin); err != nil {
		t.Fatal(err)
	}

	mcpManager := mcpclient.NewManager()
	runtime := pluginruntime.New(store, mcpManager)
	handler := NewPluginHandler(store, runtime)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	// Stop the plugin's MCP server before cleanup so Windows can remove
	// the temp dir (the process holds a lock on the binary otherwise).
	t.Cleanup(func() { runtime.Stop("test.fakemcp") })

	// Call the "structured" tool which returns text="label=<label>" plus
	// structuredContent={label, ok}.
	body, _ := json.Marshal(map[string]any{"label": "hello"})
	resp, err := http.Post(srv.URL+"/plugins/test.fakemcp/tools/structured", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		t.Fatalf("status = %d, want 200. error: %s", resp.StatusCode, errBody.Error)
	}
	var envelope struct {
		Result map[string]any `json:"result"`
		Error  string         `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error != "" {
		t.Fatalf("envelope error: %s", envelope.Error)
	}
	if envelope.Result == nil {
		t.Fatal("result is nil")
	}
	// structuredContent must be present and carry the label.
	sc, ok := envelope.Result["structuredContent"]
	if !ok {
		t.Fatalf("structuredContent missing from result: %+v", envelope.Result)
	}
	scMap, ok := sc.(map[string]any)
	if !ok {
		t.Fatalf("structuredContent is %T, want map[string]any: %+v", sc, sc)
	}
	if scMap["label"] != "hello" {
		t.Fatalf("structuredContent.label = %v, want hello", scMap["label"])
	}
	if scMap["ok"] != true {
		t.Fatalf("structuredContent.ok = %v, want true", scMap["ok"])
	}
	// Content must still be present (text part).
	content, _ := envelope.Result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("content empty: %+v", envelope.Result)
	}
	first, _ := content[0].(map[string]any)
	if first["type"] != "text" {
		t.Fatalf("content[0].type = %v, want text", first["type"])
	}
	if first["text"] != "label=hello" {
		t.Fatalf("content[0].text = %v, want label=hello", first["text"])
	}
}

// TestPluginHandlerCallToolTextOnlyResult verifies that tools without
// structuredContent still work: the bridge returns content + isError
// with structuredContent omitted (omitempty).
func TestPluginHandlerCallToolTextOnlyResult(t *testing.T) {
	if fakemcpBin == "" {
		t.Fatal("fakemcpBin not built")
	}
	storeDir := t.TempDir()
	store, err := pluginfs.New(filepath.Join(storeDir, "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	plugin := &domain.Plugin{
		Manifest: domain.PluginManifest{
			ID:      "test.fakemcp",
			Name:    "Fake MCP",
			Version: "1.0.0",
			Icon:    "test",
			MCP: domain.PluginMCPConfig{
				Transport: domain.PluginTransportStdio,
				Command:   fakemcpBin,
			},
		},
		InstallPath: filepath.Dir(fakemcpBin),
	}
	if err := store.Save(plugin); err != nil {
		t.Fatal(err)
	}

	mcpManager := mcpclient.NewManager()
	runtime := pluginruntime.New(store, mcpManager)
	handler := NewPluginHandler(store, runtime)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	// Stop the plugin's MCP server before cleanup so Windows can remove
	// the temp dir (the process holds a lock on the binary otherwise).
	t.Cleanup(func() { runtime.Stop("test.fakemcp") })

	body, _ := json.Marshal(map[string]any{"text": "world"})
	resp, err := http.Post(srv.URL+"/plugins/test.fakemcp/tools/echo", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var envelope struct {
		Result map[string]any `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Result == nil {
		t.Fatal("result is nil")
	}
	if _, ok := envelope.Result["structuredContent"]; ok {
		t.Fatalf("structuredContent should be omitted for text-only tools: %+v", envelope.Result)
	}
	content, _ := envelope.Result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("content empty: %+v", envelope.Result)
	}
	first, _ := content[0].(map[string]any)
	if first["text"] != "echo: world" {
		t.Fatalf("content[0].text = %v, want 'echo: world'", first["text"])
	}
}

// TestPluginHandlerCallToolErrorForwarded verifies that tool-level errors
// (IsError=true) are forwarded in the result body, not swallowed into a
// 502. The UI needs the content to render the tool's error message.
func TestPluginHandlerCallToolErrorForwarded(t *testing.T) {
	if fakemcpBin == "" {
		t.Fatal("fakemcpBin not built")
	}
	storeDir := t.TempDir()
	store, err := pluginfs.New(filepath.Join(storeDir, "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	plugin := &domain.Plugin{
		Manifest: domain.PluginManifest{
			ID:      "test.fakemcp",
			Name:    "Fake MCP",
			Version: "1.0.0",
			Icon:    "test",
			MCP: domain.PluginMCPConfig{
				Transport: domain.PluginTransportStdio,
				Command:   fakemcpBin,
			},
		},
		InstallPath: filepath.Dir(fakemcpBin),
	}
	if err := store.Save(plugin); err != nil {
		t.Fatal(err)
	}

	mcpManager := mcpclient.NewManager()
	runtime := pluginruntime.New(store, mcpManager)
	handler := NewPluginHandler(store, runtime)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	// Stop the plugin's MCP server before cleanup so Windows can remove
	// the temp dir (the process holds a lock on the binary otherwise).
	t.Cleanup(func() { runtime.Stop("test.fakemcp") })

	// Call a tool that does not exist → fakemcp returns a JSON-RPC error,
	// which mcp-go surfaces as a Go error (not an IsError result).
	body, _ := json.Marshal(map[string]any{})
	resp, err := http.Post(srv.URL+"/plugins/test.fakemcp/tools/nonexistent", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 for MCP-level error", resp.StatusCode)
	}
	var envelope struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error == "" {
		t.Fatal("expected error message for unknown tool")
	}
}
