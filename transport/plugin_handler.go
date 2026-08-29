package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"nusashell/application"
	"nusashell/contracts"
	"nusashell/domain"
)

// PluginHandler serves plugin UI static files and routes tool calls
// from plugin UIs to the plugin's MCP server. It injects a
// window.shell shim into HTML responses so plugins work without
// modification.
//
// Routes:
//
//	GET  /plugins                       → list installed plugins
//	GET  /plugins/{id}/                 → serve plugin UI (index.html)
//	GET  /plugins/{id}/ui/...           → serve UI static files
//	GET  /plugins/{id}/tools            → list tools (JSON)
//	POST /plugins/{id}/tools/{tool}     → call a tool (JSON body = args)
type PluginHandler struct {
	Store   application.PluginUIPort
	Runtime application.PluginRuntimePort
}

// NewPluginHandler creates a plugin handler backed by the given store
// and runtime manager ports.
func NewPluginHandler(store application.PluginUIPort, runtime application.PluginRuntimePort) *PluginHandler {
	return &PluginHandler{Store: store, Runtime: runtime}
}

// RegisterRoutes registers all plugin routes on the given mux.
func (h *PluginHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /plugins", h.handleList)
	mux.HandleFunc("GET /plugins/", h.handlePlugin)
	mux.HandleFunc("POST /plugins/", h.handlePlugin)
}

// handleList returns a JSON list of installed plugins.
func (h *PluginHandler) handleList(w http.ResponseWriter, r *http.Request) {
	plugins, err := h.Store.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]contracts.PluginUIEntryDTO, 0, len(plugins))
	for _, p := range plugins {
		out = append(out, pluginToDTO(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": out})
}

// handlePlugin dispatches to the right sub-handler based on the path.
func (h *PluginHandler) handlePlugin(w http.ResponseWriter, r *http.Request) {
	// Path: /plugins/{id}/...
	rest := strings.TrimPrefix(r.URL.Path, "/plugins/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	pluginID := parts[0]
	subPath := ""
	if len(parts) > 1 {
		subPath = parts[1]
	}

	// Route: POST /plugins/{id}/tools/{tool}
	if r.Method == http.MethodPost && strings.HasPrefix(subPath, "tools/") {
		toolName := strings.TrimPrefix(subPath, "tools/")
		if toolName == "" {
			http.NotFound(w, r)
			return
		}
		h.handleCallTool(w, r, pluginID, toolName)
		return
	}

	// Route: GET /plugins/{id}/tools
	if r.Method == http.MethodGet && subPath == "tools" {
		h.handleListTools(w, r, pluginID)
		return
	}

	// Route: GET /plugins/{id}/... → serve static UI files
	if r.Method == http.MethodGet {
		h.handleStatic(w, r, pluginID, subPath)
		return
	}

	http.NotFound(w, r)
}

// handleCallTool routes a tool call to the plugin's MCP server.
// Body is JSON args. Response is the full plugin tool result
// (content + structuredContent + isError) so plugin UIs that expect
// structured JSON do not have to parse human-readable text.
func (h *PluginHandler) handleCallTool(w http.ResponseWriter, r *http.Request, pluginID, toolName string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
		return
	}
	var args map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &args); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON args: " + err.Error()})
			return
		}
	}
	if args == nil {
		args = map[string]any{}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	result, err := h.Runtime.CallTool(ctx, pluginID, toolName, args)
	if err != nil {
		// Surface MCP-level errors (e.g. server not connected) as 502.
		// Tool-level errors (IsError=true) are forwarded in the result
		// body so the UI can render the tool's error message.
		if result == nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

// handleListTools returns the tools advertised by a running plugin.
func (h *PluginHandler) handleListTools(w http.ResponseWriter, r *http.Request, pluginID string) {
	// Ensure the plugin is started.
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	_, err := h.Runtime.EnsureStarted(ctx, pluginID)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	tools := h.Runtime.ListTools(pluginID)
	if tools == nil {
		tools = []contracts.MCPToolDTO{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": tools})
}

// handleStatic serves plugin UI files. HTML responses get the
// window.shell shim injected so plugins work without modification.
//
// The URL path /plugins/{id}/<rest> maps to the plugin's UI directory.
// If the manifest's ui.entry is "ui/index.html", the UI directory is
// "ui/" and a request for /plugins/{id}/style.css resolves to
// "ui/style.css". A request for /plugins/{id}/ (empty rest) resolves
// to the UI entry file (e.g. "ui/index.html").
func (h *PluginHandler) handleStatic(w http.ResponseWriter, r *http.Request, pluginID, subPath string) {
	plugin, err := h.Store.Get(pluginID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !plugin.HasUI {
		http.Error(w, "plugin has no UI", http.StatusNotFound)
		return
	}
	uiDir := h.Store.UIDir(plugin)
	if uiDir == "" {
		http.Error(w, "plugin has no UI directory", http.StatusNotFound)
		return
	}
	// Default to index.html when subPath is empty.
	filePath := subPath
	if filePath == "" || strings.HasSuffix(filePath, "/") {
		filePath = path.Join(filePath, "index.html")
	}
	// Security: prevent path traversal outside the UI directory.
	cleanPath := path.Clean("/" + filePath)
	fullPath := filepath.Join(uiDir, cleanPath)
	// Ensure the resolved path is within uiDir.
	if !strings.HasPrefix(fullPath, uiDir+string(filepath.Separator)) && fullPath != uiDir {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// If this is an HTML file, read it, inject the shim, and serve.
	if strings.HasSuffix(fullPath, ".html") {
		data, err := readFile(fullPath)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		injected := injectShim(data, pluginID)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(injected)
		return
	}

	// Non-HTML files: serve directly from the filesystem.
	http.ServeFile(w, r, fullPath)
}

// pluginToDTO converts a domain.Plugin to a contracts.PluginUIEntryDTO. The
// icon is left empty here; icon resolution to a PNG data URL is an
// infrastructure concern (pluginicon) and is applied by the
// pluginfs.ToDTO helper used by the application resources handler. The
// plugin UI handler serves icons as static files from the UI directory,
// so it does not need the data URL.
func pluginToDTO(p *domain.Plugin) contracts.PluginUIEntryDTO {
	return contracts.PluginUIEntryDTO{
		ID:          p.Manifest.ID,
		Name:        p.Manifest.Name,
		Version:     p.Manifest.Version,
		Category:    p.Manifest.Category,
		HasUI:       p.HasUI,
		InstallPath: p.InstallPath,
		InstalledAt: p.InstalledAt,
		Manifest:    &p.Manifest,
	}
}

// readFile is a helper that reads a file and returns its contents.
func readFile(path string) ([]byte, error) {
	f, err := openFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// openFile opens a file for reading.
func openFile(path string) (io.ReadCloser, error) {
	return os.Open(path)
}

// injectShim inserts the window.shell shim into the <head> of an HTML
// document and relaxes CSP connect-src so fetch() to same-origin works.
// If there is no <head>, the shim is prepended to the document.
func injectShim(html []byte, pluginID string) []byte {
	content := string(html)
	shim := fmt.Sprintf(`<script>
(function() {
  var PLUGIN_ID = %q;
  window.shell = window.shell || {};
  window.shell.callTool = function(pluginId, toolName, args) {
    return fetch("/plugins/" + pluginId + "/tools/" + toolName, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(args || {})
    }).then(function(res) {
      if (!res.ok) return res.json().then(function(e) { throw new Error(e.error || res.statusText); });
      return res.json().then(function(r) { return r.result; });
    });
  };
  window.shell.listTools = function(pluginId) {
    return fetch("/plugins/" + pluginId + "/tools").then(function(res) { return res.json(); });
  };
  window.shell.build = "go";
})();
</script>`, pluginID)

	// Relax CSP: replace connect-src 'none' with connect-src 'self'
	// so the shim's fetch() calls are allowed.
	content = strings.Replace(content, "connect-src 'none'", "connect-src 'self'", 1)

	// Inject the shim right after <head> or at the start of the document.
	headIdx := strings.Index(strings.ToLower(content), "<head>")
	if headIdx >= 0 {
		insertAt := headIdx + len("<head>")
		var buf bytes.Buffer
		buf.WriteString(content[:insertAt])
		buf.WriteString("\n")
		buf.WriteString(shim)
		buf.WriteString("\n")
		buf.WriteString(content[insertAt:])
		return buf.Bytes()
	}
	// No <head> — prepend.
	var buf bytes.Buffer
	buf.WriteString(shim)
	buf.WriteString("\n")
	buf.WriteString(content)
	return buf.Bytes()
}
