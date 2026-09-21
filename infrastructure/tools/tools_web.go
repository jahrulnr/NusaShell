package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"nusashell/application"
	"nusashell/domain"
	"nusashell/pkg/httpclient"

	"github.com/jahrulnr/searchwire"
)

func (t *Toolbox) webToolEntries() []toolEntry {
	return []toolEntry{
		{
			info:    application.ToolInfo{Name: "web_search", Description: "Search the web for fresh information. With an active Codex chat provider, Codex search is tried first and searchwire is the fallback; other providers use searchwire across Brave, Serper, Tavily, Startpage, Wikipedia, and GitHub. Returns ranked results with title, URL, and snippet. Follow up with web_fetch on promising URLs for full page content. Oversized result lists are truncated in-band (~32KiB) with overflow_path pointing at the full JSONL in the platform temp dir; continue with file_read.", InputSchema: obj("object", props("query", str("Search query"), "limit", intSchema("Max results (default 10)")), "query")},
			handler: t.execWebSearch,
		},
		{
			info:    application.ToolInfo{Name: "web_fetch", Description: "Fetch a public URL and return readable text (HTML stripped to title + visible text). Use after web_search to read full page content from a result URL. Accepts http/https only. Hostnames resolve through Cloudflare/Google public DNS with A+AAAA support; private, loopback, link-local, ULA, multicast, unspecified, and redirect destinations are rejected. Extraction may read up to max_bytes (default 2MB); the in-band result is capped at ~32KiB. When truncated, overflow_path is an absolute temp file — continue with file_read using next_offset_bytes.", InputSchema: obj("object", props("url", str("Public http/https URL to fetch"), "max_bytes", intSchema("Optional max bytes of extracted text (default 2MB)")), "url")},
			handler: t.execWebFetch,
		},
	}
}

func (t *Toolbox) webAnswerToolEntry() toolEntry {
	return toolEntry{
		info: application.ToolInfo{
			Name:        "web_answer",
			InputSchema: obj("object", props("question", str("Question to answer"), "provider", str("Optional provider id; omit for the default priority order")), "question"),
		},
		handler:  t.execWebAnswer,
		enabled:  webAnswerAdvertised,
		describe: webAnswerDescription,
	}
}

// webAnswerAdvertised reports whether web_answer is advertised: a searchwire
// searcher can be built from Settings → Web Answer + the stored API key and
// has at least one configured answer provider.
func webAnswerAdvertised(t *Toolbox) bool {
	sw := t.webAnswerSearcher()
	return sw != nil && sw.CanAnswer()
}

// webAnswerDescription builds the advertised description; called by
// ListTools only after webAnswerAdvertised passes, so the searcher has at
// least one answer provider.
func webAnswerDescription(t *Toolbox) string {
	providers := t.webAnswerSearcher().AvailableAnswerProviders()
	providerList := strings.Join(providers, ", ")
	return "Get a web-grounded answer to a question using an LLM with built-in web search. Use when you need a synthesized answer rather than raw search results. Available providers: " + providerList + ". Omit provider to use the default priority order."
}

// webAnswerSearcher builds a searchwire.Searcher on-demand from the web
// answer settings + stored API key. Returns nil if web_answer is not
// configured (no provider or no API key).
func (t *Toolbox) webAnswerSearcher() *searchwire.Searcher {
	if t.Settings == nil || t.Credentials == nil {
		return nil
	}
	s := t.Settings.Get()
	provider := strings.TrimSpace(s.WebAnswerProvider)
	if provider == "" {
		return nil
	}
	key, has, _ := t.Credentials.Get("web_answer")
	if !has || strings.TrimSpace(key) == "" {
		return nil
	}
	model := strings.TrimSpace(s.WebAnswerModel)
	disabled := false
	cfg := searchwire.Config{
		HTTPClient: httpclient.NewPublic(),
		Timeout:    120 * time.Second,
		// Explicitly disable all answer providers so env var fallback
		// (e.g. OPENROUTER_API_KEY) doesn't silently enable a provider
		// the user didn't select in Settings.
		OpenRouter: searchwire.OpenRouterConfig{Enabled: &disabled},
		OpenAI:     searchwire.OpenAIConfig{Enabled: &disabled},
		Perplexity: searchwire.PerplexityConfig{Enabled: &disabled},
		Anthropic:  searchwire.AnthropicConfig{Enabled: &disabled},
		XAI:        searchwire.XAIConfig{Enabled: &disabled},
	}
	switch provider {
	case "brave":
		cfg.Brave = searchwire.BraveConfig{APIKey: key}
	case "openrouter":
		cfg.OpenRouter = searchwire.OpenRouterConfig{Enabled: ptrBool(true), APIKey: key, Model: model}
	case "openai":
		cfg.OpenAI = searchwire.OpenAIConfig{Enabled: ptrBool(true), APIKey: key, Model: model}
	case "perplexity":
		cfg.Perplexity = searchwire.PerplexityConfig{Enabled: ptrBool(true), APIKey: key, Preset: model}
	case "anthropic":
		cfg.Anthropic = searchwire.AnthropicConfig{Enabled: ptrBool(true), APIKey: key, Model: model}
	case "xai":
		cfg.XAI = searchwire.XAIConfig{Enabled: ptrBool(true), APIKey: key, Model: model}
	default:
		return nil
	}
	return searchwire.New(cfg)
}

// webSearchSearcher builds the searchwire.Searcher for web_search on
// demand from the credential store, so provider API keys apply without a
// restart (searchwire falls back to the standard env vars per call).
func (t *Toolbox) webSearchSearcher() *searchwire.Searcher {
	return searchwire.New(SearchwireSearchConfig(t.Credentials))
}

// webSearchSources resolves the Settings WebSearchStrategy into a per-call
// searchwire source restriction for the given searcher:
//
//   - ""/auto: nil — every registered source merges (default)
//   - round_robin: one API-keyed provider (brave, serper, tavily) that
//     resolves a key (stored or env), rotating in registration order per
//     query to spread paid-API quota; nil when none carry a key
//   - random: one keyed provider picked at random per query; nil when none
//     carry a key
//   - a bare source name: pin the query to that source when registered;
//     nil (all sources) when it is not — e.g. a keyed provider whose API
//     key is not configured yet
func (t *Toolbox) webSearchSources(strategy string, searcher *searchwire.Searcher) []string {
	if searcher == nil {
		return nil
	}
	strategy = strings.TrimSpace(strategy)
	if strategy == "" || strategy == domain.WebSearchStrategyAuto {
		return nil
	}
	registered := searcher.Sources()
	if strategy == domain.WebSearchStrategyRoundRobin || strategy == domain.WebSearchStrategyRandom {
		pool := make([]string, 0, len(webSearchKeyEnv))
		for _, p := range webSearchKeyEnv {
			if containsString(registered, p.name) && webSearchResolvedKey(t.Credentials, p.id, p.env) != "" {
				pool = append(pool, p.name)
			}
		}
		if len(pool) == 0 {
			return nil
		}
		if strategy == domain.WebSearchStrategyRoundRobin {
			n := t.webSearchRR.Add(1) - 1
			return []string{pool[n%uint64(len(pool))]}
		}
		return []string{pool[rand.IntN(len(pool))]}
	}
	if containsString(registered, strategy) {
		return []string{strategy}
	}
	return nil
}

func (t *Toolbox) execWebSearch(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.Query) == "" {
		return "", fmt.Errorf("query is required")
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}
	codexFallback := false
	codexCtx, cancelCodex := context.WithTimeout(ctx, webSearchRequestTimeout)
	codexResponse, codexAttempted, codexErr := t.codexWebSearch(codexCtx, args.Query, limit)
	cancelCodex()
	if codexAttempted {
		if codexErr == nil {
			return formatCodexWebSearch(codexResponse, limit), nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		codexFallback = true
	}
	// A fresh searcher per call keeps the provider API keys and the
	// strategy from Settings live without a restart. The startup
	// searcher remains the fallback when settings are unavailable.
	searcher := t.webSearchSearcher()
	if searcher == nil {
		searcher = t.Searcher
	}
	if searcher == nil {
		return "", depMissing("search is not available")
	}
	strategy := ""
	if t.Settings != nil {
		strategy = t.Settings.Get().WebSearchStrategy
	}
	opts := searchwire.SearchOptions{
		Limit:   limit,
		Sources: t.webSearchSources(strategy, searcher),
	}
	var resp *searchwire.Response
	var err error
	if t.searchwireSearch != nil {
		resp, err = t.searchwireSearch(ctx, searcher, args.Query, opts)
	} else {
		resp, err = searcher.SearchWithOptions(ctx, args.Query, opts)
	}
	if err != nil {
		if codexErr != nil {
			return "", fmt.Errorf("Codex search failed: %v; searchwire fallback failed: %w", codexErr, err)
		}
		return "", fmt.Errorf("search failed: %w", err)
	}
	if limit > len(resp.Results) {
		limit = len(resp.Results)
	}
	items := make([]any, 0, limit)
	for _, r := range resp.Results[:limit] {
		items = append(items, map[string]any{"title": r.Title, "url": r.URL, "snippet": r.Snippet, "sources": r.Sources})
	}
	meta := map[string]any{"count": len(items)}
	if strategy != "" {
		meta["strategy"] = strategy
	}
	if len(opts.Sources) == 1 {
		meta["provider"] = opts.Sources[0]
	}
	if codexFallback {
		meta["fallback_from"] = "codex"
		if _, ok := meta["provider"]; !ok {
			meta["provider"] = "searchwire"
		}
	}
	if len(resp.Errors) > 0 {
		var errs []any
		for _, e := range resp.Errors {
			errs = append(errs, map[string]any{"source": e.Source, "error": e.Error})
		}
		meta["errors"] = errs
	}
	return capJSONL("web_search", meta, items), nil
}

func (t *Toolbox) execWebFetch(ctx context.Context, argsJSON []byte) (string, error) {
	if t.Searcher == nil {
		return "", depMissing("search is not available")
	}
	var args struct {
		URL      string `json:"url"`
		MaxBytes int64  `json:"max_bytes"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.URL) == "" {
		return "", fmt.Errorf("url is required")
	}
	page, err := t.Searcher.FetchWithLimit(ctx, args.URL, args.MaxBytes)
	if err != nil {
		var httpErr *searchwire.HTTPError
		if errors.As(err, &httpErr) && httpErr.RetryAfter > 0 {
			return "", fmt.Errorf("fetch failed: %w (retry after %ds)", err, httpErr.RetryAfter)
		}
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	var sb strings.Builder
	meta := map[string]any{
		"status":       page.StatusCode,
		"content_type": page.ContentType,
		"truncated":    page.Truncated,
	}
	if page.FinalURL != page.URL {
		meta["final_url"] = page.FinalURL
	}
	if page.Redirects > 0 {
		meta["redirects"] = page.Redirects
	}
	if page.Title != "" {
		meta["title"] = page.Title
	}
	var linkItems []any
	if len(page.Links) > 0 {
		for _, l := range page.Links {
			if len(linkItems) >= 50 {
				break
			}
			linkItems = append(linkItems, map[string]any{"text": strings.TrimSpace(l.Text), "href": l.Href})
		}
		meta["links_count"] = len(page.Links)
	}
	if page.Truncated {
		meta["bytes"] = page.Bytes
	}
	sb.WriteString(page.Text)
	// Page text is prose body; links are structured JSONL appended
	// after the text so the agent can parse them independently.
	body := sb.String()
	if len(linkItems) > 0 {
		var linkLines []string
		for _, li := range linkItems {
			b, _ := json.Marshal(li)
			linkLines = append(linkLines, string(b))
		}
		body = body + "\n\n" + strings.Join(linkLines, "\n")
	}
	return capToolOutput("web_fetch", meta, body), nil
}

func (t *Toolbox) execWebAnswer(ctx context.Context, argsJSON []byte) (string, error) {
	sw := t.webAnswerSearcher()
	if sw == nil || !sw.CanAnswer() {
		return "", depMissing("web_answer is not configured — set a provider and API key in Settings → Web Answer")
	}
	var args struct {
		Question string `json:"question"`
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.Question) == "" {
		return "", fmt.Errorf("question is required")
	}
	opts := searchwire.AnswerOptions{Provider: strings.TrimSpace(args.Provider)}
	answer, err := sw.AnswerWithOptions(ctx, args.Question, opts)
	if err != nil {
		return "", fmt.Errorf("answer failed: %w", err)
	}
	return yamlMD(map[string]any{"provider": answer.Provider}, answer.Text), nil
}
