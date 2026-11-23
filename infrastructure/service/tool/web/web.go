// Package toolweb adapts searchwire tool definitions into the domain tool
// port. Searchwire owns the tool semantics (names, descriptions, schemas,
// execution); this package only translates the contract — and carries the
// adapter concerns searchwire must not own: per-turn provider selection,
// conversation-owned output spilling, and document post-processing.
package toolweb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jahrulnr/searchwire"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

const (
	// fetchTextBudget is the searchwire-side text cap for fetch — under the
	// domain's 32000-byte tool output budget so the JSON envelope (links,
	// llmsTxt, headers) fits alongside it. Larger pages spill to a
	// conversation artifact instead of silently eliding mid-document.
	fetchTextBudget = 20000
	// maxArtifactBytes bounds a spill copy — artifacts are extracted text,
	// already bounded upstream by searchwire's raw body limit.
	maxArtifactBytes = 32 << 20
	// backendFallbackMaxLen bounds the provider-fallback note embedded in
	// the output document.
	backendFallbackMaxLen = 200
)

// Tool runs one searchwire tool definition under the domain contract.
// When the backend policy names a provider that exposes the same
// capability as a plain API (OmniRoute, OpenCode), Execute prefers that
// endpoint with the provider's own credentials and falls back to
// searchwire on any failure.
type Tool struct {
	name       string // searchwire's tool name: search, fetch, answer
	schema     valueobject.ToolSchema
	off        valueobject.ToolSchema // the same schema, disabled
	outputs    drepo.ToolOutput       // conversation-owned spill store; may be nil
	settings   service.WebSearchConfigurer
	httpClient *http.Client // test seam; nil uses the default client

	// fixed runs a given definition instead of one built from settings —
	// New's tools, which wrap a definition the caller made.
	fixed func(context.Context, json.RawMessage) (json.RawMessage, error)

	// searcher is built from the settings it was made for and rebuilt
	// when they change, so connections are reused between calls.
	searcherMu  sync.Mutex
	searcherFor service.WebSearchSettings
	searcher    *searchwire.Searcher

	// webSearchUnsupported records endpoint|model pairs that answered a
	// Responses web_search call with zero web_search_call items — the
	// capability is absent, not transient, so later calls skip the probe
	// instead of paying a doomed provider round-trip per search.
	unsupportedMu    sync.Mutex
	unsupportedProbe map[string]bool
}

// Tools adapts the named searchwire capabilities (searchwire.ToolSearch,
// ToolFetch, ToolAnswer) into domain tools whose behavior follows the
// owner's live settings: each call reads them, so turning the tools off,
// adding a key, or changing a limit applies without a restart. A nil
// settings source runs on the library's defaults. Names advertised to
// models are prefixed "web_" (web_search, web_fetch, …) so they read as
// web capabilities rather than generic verbs.
func Tools(outputs drepo.ToolOutput, settings service.WebSearchConfigurer, names ...string) ([]service.Tool, error) {
	want := make(map[string]bool, len(names))
	for _, name := range names {
		want[strings.ToLower(strings.TrimSpace(name))] = true
	}
	var tools []service.Tool
	for _, def := range searchwire.New(searchwire.Config{}).Tools() {
		if len(want) > 0 && !want[def.Name] {
			continue
		}
		tool, err := newTool(def, outputs)
		if err != nil {
			return nil, err
		}
		tool.settings = settings
		tools = append(tools, tool)
	}
	return tools, nil
}

// New wraps one fixed searchwire ToolDef: its Run serves every call. The
// advertised name gains a "web_" prefix when it does not already carry one.
func New(def searchwire.ToolDef, outputs drepo.ToolOutput) (service.Tool, error) {
	tool, err := newTool(def, outputs)
	if err != nil {
		return nil, err
	}
	tool.fixed = def.Run
	return tool, nil
}

func newTool(def searchwire.ToolDef, outputs drepo.ToolOutput) (*Tool, error) {
	name := def.Name
	if !strings.HasPrefix(name, "web_") {
		name = "web_" + name
	}
	schema, err := valueobject.NewToolSchema(name, def.Description, def.InputSchema, true, enum.ToolPermissionAgent)
	if err != nil {
		return nil, err
	}
	off, err := valueobject.NewToolSchema(name, def.Description, def.InputSchema, false, enum.ToolPermissionAgent)
	if err != nil {
		return nil, err
	}
	return &Tool{name: def.Name, schema: schema, off: off, outputs: outputs}, nil
}

// Schema is disabled while the owner has the web tools turned off, so
// the model is not offered them. Settings that cannot be read keep the
// tools offered; Execute reports the problem.
func (t *Tool) Schema() valueobject.ToolSchema {
	if t.settings != nil {
		if st, err := t.settings.WebSearchSettings(context.Background()); err == nil && !st.Enabled {
			return t.off
		}
	}
	return t.schema
}

// defaults are the settings without a settings source: every keyless
// source on, the library's numbers.
var defaults = service.WebSearchSettings{
	Enabled: true, Backend: service.WebBackendAuto,
	Brave: service.WebSearchSource{Enabled: true}, GitHub: service.WebSearchGitHub{Enabled: true, SearchIssues: true},
}

func (t *Tool) current(ctx context.Context) (service.WebSearchSettings, error) {
	if t.settings == nil {
		return defaults, nil
	}
	return t.settings.WebSearchSettings(ctx)
}

// run executes the searchwire side: the fixed definition, or the one of a
// searcher built for st.
func (t *Tool) run(ctx context.Context, st service.WebSearchSettings, args json.RawMessage) (json.RawMessage, error) {
	if t.fixed != nil {
		return t.fixed(ctx, args)
	}
	// Backend and provider do not shape the searcher; leave them out of
	// the comparison so switching backends keeps the connections.
	key := st
	key.Enabled, key.Backend, key.Provider = false, "", service.WebProvider{}
	t.searcherMu.Lock()
	if t.searcher == nil || t.searcherFor != key {
		t.searcher = searchwire.New(searcherConfig(st))
		t.searcherFor = key
	}
	searcher := t.searcher
	t.searcherMu.Unlock()
	for _, def := range searcher.Tools() {
		if def.Name == t.name {
			return def.Run(ctx, args)
		}
	}
	return nil, fmt.Errorf("searchwire offers no %q tool", t.name)
}

// searcherConfig maps the settings onto the library's config. The fetch
// text budget defaults under the domain's 32000-byte tool output budget.
func searcherConfig(st service.WebSearchSettings) searchwire.Config {
	maxText := st.MaxResponseBytes
	if maxText <= 0 {
		maxText = fetchTextBudget
	}
	return searchwire.Config{
		UserAgent:           st.UserAgent,
		Limit:               st.Limit,
		Timeout:             st.Timeout,
		MaxResponseBytes:    maxText,
		MaxRawResponseBytes: st.MaxRawBytes,
		Brave:               searchwire.BraveConfig{Enabled: &st.Brave.Enabled, APIKey: st.Brave.Key},
		Serper:              searchwire.SerperConfig{Enabled: &st.Serper.Enabled, APIKey: st.Serper.Key},
		Tavily:              searchwire.TavilyConfig{Enabled: &st.Tavily.Enabled, APIKey: st.Tavily.Key},
		GitHub:              searchwire.GitHubConfig{Enabled: &st.GitHub.Enabled, Token: st.GitHub.Token, SearchIssues: &st.GitHub.SearchIssues},
	}
}

func (t *Tool) Execute(ctx context.Context, call entity.ToolCall, _ service.ToolEmitter) (service.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return service.ToolResult{}, err
	}
	st, err := t.current(ctx)
	if err != nil {
		return service.ToolResult{}, err
	}
	if !st.Enabled {
		return service.ToolResult{}, apperror.New(409, "web tools are turned off — Settings → Web search", nil)
	}
	args := json.RawMessage(call.Input())
	// The backend policy decides which provider, if any, may serve the
	// call natively; the provider's own kind decides whether it can.
	var (
		p  service.WebProvider
		ok bool
	)
	switch st.Backend {
	case service.WebBackendSearchwire:
	case service.WebBackendProvider:
		p, ok = st.Provider, st.Provider.Endpoint != ""
	default:
		p, ok = service.WebProviderFrom(ctx)
	}
	var fallbackNote string
	if ok && p.Endpoint != "" {
		var (
			out     json.RawMessage
			err     error
			backend string
		)
		switch {
		case omniRouteKinds[p.Kind]:
			client := t.omniRouteClient()
			switch t.name {
			case searchwire.ToolSearch:
				out, err = client.search(ctx, p, args)
			case searchwire.ToolFetch:
				out, err = client.fetch(ctx, p, args)
			}
			backend = omniRouteBackend
		case openCodeKinds[p.Kind] && t.name == searchwire.ToolSearch && p.Model != "":
			probeKey := p.Endpoint + "\x00" + p.Model
			if !t.probeFailed(probeKey) {
				out, err = t.openCodeClient().search(ctx, p, args)
				backend = openCodeBackend
				if errors.Is(err, errNoWebSearchCall) {
					t.markProbeFailed(probeKey)
				}
			} else {
				// Skipped, not attempted — say so instead of leaving the
				// model guessing why the provider backend stayed silent.
				fallbackNote = "provider native search skipped — this model does not execute web_search (learned from a previous call)"
			}
		}
		if err == nil && out != nil {
			return t.result(ctx, call, out, backend, "")
		}
		if backend != "" && fallbackNote == "" {
			fallbackNote = sanitizeFallbackNote(err, p)
		}
	}
	out, err := t.run(ctx, st, args)
	if err != nil {
		if msg := scrubKeys(err.Error(), st); msg != err.Error() {
			return service.ToolResult{}, errors.New(msg)
		}
		return service.ToolResult{}, err
	}
	return t.result(ctx, call, json.RawMessage(scrubKeys(string(out), st)), searchwireBackend, fallbackNote)
}

// scrubKeys masks the sources' keys: a source's error can echo its
// request, and both the error and the document reach the model.
func scrubKeys(s string, st service.WebSearchSettings) string {
	for _, k := range []string{st.Brave.Key, st.Serper.Key, st.Tavily.Key, st.GitHub.Token} {
		if len(k) >= 4 {
			s = strings.ReplaceAll(s, k, "[api key]")
		}
	}
	return s
}

// result annotates the emitted document with the backend that served it,
// moves a searchwire tempFile artifact into the conversation's tool-output
// store (the library's temp-dir path is unreachable for the turn's file
// tools — the spill path is reachable via exec like every other tool's),
// and spills oversized documents the way exec does so the middle-truncated
// text keeps a recovery pointer instead of silently eliding content.
func (t *Tool) result(ctx context.Context, call entity.ToolCall, out json.RawMessage, backend, fallbackNote string) (service.ToolResult, error) {
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		return service.ToolResult{Text: string(out)}, nil
	}
	doc["backend"] = backend
	if fallbackNote != "" {
		doc["backendFallback"] = fallbackNote
	}
	if src, _ := doc["tempFile"].(string); src != "" {
		if ref := t.respillArtifact(ctx, call, src); ref != "" {
			doc["tempFile"] = ref
		}
	}
	buf, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return service.ToolResult{Text: string(out)}, nil
	}
	if len(buf) > call.OutputBudget() {
		if ref := t.spillBytes(ctx, call, buf); ref != "" {
			return service.ToolResult{Text: call.OutputWithMiddleTruncateWindow(string(buf), string(buf), len(buf), "full output: "+ref)}, nil
		}
	}
	return service.ToolResult{Text: string(buf)}, nil
}

// reserve allocates a conversation-owned spill path; nil when no usable
// conversation or output store is bound (tests, detached contexts).
func (t *Tool) reserve(ctx context.Context, call entity.ToolCall) drepo.ToolOutputSpill {
	if t.outputs == nil {
		return nil
	}
	convID := service.ConversationID(ctx)
	if err := valueobject.ValidID(convID); err != nil {
		return nil
	}
	spill, err := t.outputs.Reserve(ctx, convID, call.ID())
	if err != nil {
		return nil
	}
	return spill
}

// spillBytes writes buf to a reserved conversation spill file and returns
// its reference — "" on any failure, and the caller keeps the inline doc.
func (t *Tool) spillBytes(ctx context.Context, call entity.ToolCall, buf []byte) string {
	spill := t.reserve(ctx, call)
	if spill == nil {
		return ""
	}
	f, err := spill.Open()
	if err != nil {
		return ""
	}
	_, werr := f.Write(buf)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return ""
	}
	return spill.Reference()
}

// respillArtifact copies a searchwire temp artifact into the conversation's
// tool-output store, returning the reachable reference. The original file
// is removed on success so spent artifacts do not accumulate.
func (t *Tool) respillArtifact(ctx context.Context, call entity.ToolCall, srcPath string) string {
	spill := t.reserve(ctx, call)
	if spill == nil {
		return ""
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return ""
	}
	defer src.Close()
	f, err := spill.Open()
	if err != nil {
		return ""
	}
	_, werr := io.Copy(f, io.LimitReader(src, maxArtifactBytes))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return ""
	}
	_ = os.Remove(srcPath)
	return spill.Reference()
}

func boundedErrorText(err error) string {
	s := strings.TrimSpace(err.Error())
	if len(s) > backendFallbackMaxLen {
		return s[:backendFallbackMaxLen] + "…"
	}
	return s
}

// sanitizeFallbackNote strips provider internals from the note embedded in
// the tool document — the endpoint URL and model id are wiring details the
// model never needs, and leaving them in leaks them into context and UI.
func sanitizeFallbackNote(err error, p service.WebProvider) string {
	s := ""
	if err != nil {
		s = boundedErrorText(err)
	}
	// The key first: a provider error body can echo the request's
	// Authorization header, and the note lands in tool output.
	if p.APIKey != "" {
		s = strings.ReplaceAll(s, p.APIKey, "<api key>")
	}
	if p.Endpoint != "" {
		s = strings.ReplaceAll(s, p.Endpoint, "<provider endpoint>")
	}
	if p.Model != "" {
		s = strings.ReplaceAll(s, p.Model, "<model>")
	}
	return s
}

// probeFailed reports whether this endpoint|model already proved it cannot
// run the web_search server tool.
func (t *Tool) probeFailed(key string) bool {
	t.unsupportedMu.Lock()
	defer t.unsupportedMu.Unlock()
	return t.unsupportedProbe[key]
}

func (t *Tool) markProbeFailed(key string) {
	t.unsupportedMu.Lock()
	defer t.unsupportedMu.Unlock()
	if t.unsupportedProbe == nil {
		t.unsupportedProbe = map[string]bool{}
	}
	t.unsupportedProbe[key] = true
}

// Defaults are the values the tools run on when a setting is 0 or empty:
// the search library's own, and this adapter's fetch text budget.
type Defaults struct {
	Limit            int
	Timeout          time.Duration
	MaxResponseBytes int64
	MaxRawBytes      int64
	UserAgent        string
}

// LibraryDefaults mirrors searchwire's unexported defaults (searchwire.go)
// for display; the tools themselves pass zeros through to the library.
var LibraryDefaults = Defaults{
	Limit:            10,
	Timeout:          10 * time.Second,
	MaxResponseBytes: fetchTextBudget,
	MaxRawBytes:      10 << 20,
	UserAgent:        "searchwire/0 (+https://github.com/jahrulnr/searchwire)",
}

// NativeSearch reports whether a provider id names a provider whose own
// endpoint can serve web search: OmniRoute and 9Router as is, OpenCode Zen
// and Go through a model, which needsModel says.
func NativeSearch(providerID string) (native, needsModel bool) {
	k := strings.ToLower(providerID)
	return omniRouteKinds[k] || openCodeKinds[k], openCodeKinds[k]
}
