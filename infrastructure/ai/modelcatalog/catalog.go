// Package modelcatalog fetches and caches the models.dev public catalog
// (https://models.dev/api.json) — a key-less, open-source metadata source
// for 100+ AI providers. It provides context window, pricing, capabilities
// (reasoning, tool call, structured output, vision), and release dates
// that provider /models endpoints often don't return.
//
// The catalog is fetched once and cached in memory for 1 hour. Lookups
// are O(1) via a flat index keyed by model ID (with and without provider
// prefix). This enrichment runs after the provider's own /models import,
// filling in gaps without requiring API keys.
package modelcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"nusashell/infrastructure/config"
)

// CatalogURL is the public, key-less models.dev endpoint.
const CatalogURL = "https://models.dev/api.json"

// CacheTTL is how long the fetched catalog stays fresh.
const CacheTTL = 1 * time.Hour

// ModelMetadata is the enriched metadata for a single model from the catalog.
type ModelMetadata struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Context          int      `json:"context"`
	Output           int      `json:"output"`
	InputCost        float64  `json:"input_cost"`  // USD per 1M tokens
	OutputCost       float64  `json:"output_cost"` // USD per 1M tokens
	CacheReadCost    float64  `json:"cache_read_cost"`
	Reasoning        bool     `json:"reasoning"`
	ToolCall         bool     `json:"tool_call"`
	StructuredOutput bool     `json:"structured_output"`
	Vision           bool     `json:"vision"`
	Audio            bool     `json:"audio"`
	Video            bool     `json:"video"`
	Temperature      bool     `json:"temperature"`
	SupportedEfforts []string `json:"supported_efforts"`
	KnowledgeCutoff  string   `json:"knowledge_cutoff"`
	ReleaseDate      string   `json:"release_date"`
	Kind             string   `json:"kind"` // chat|embedding|image|video|tts|stt
}

// catalogProvider is one provider entry in the models.dev JSON.
type catalogProvider struct {
	ID     string                  `json:"id"`
	Name   string                  `json:"name"`
	API    string                  `json:"api"`
	Models map[string]catalogModel `json:"models"`
}

// catalogModel is one model entry in the models.dev JSON.
type catalogModel struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	Reasoning        bool              `json:"reasoning"`
	ToolCall         bool              `json:"tool_call"`
	StructuredOutput bool              `json:"structured_output"`
	Temperature      bool              `json:"temperature"`
	Attachment       bool              `json:"attachment"`
	Knowledge        string            `json:"knowledge"`
	ReleaseDate      string            `json:"release_date"`
	ReasoningOptions []reasoningOption `json:"reasoning_options"`
	Modalities       struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	Cost struct {
		Input     float64 `json:"input"`
		Output    float64 `json:"output"`
		CacheRead float64 `json:"cache_read"`
	} `json:"cost"`
}

type reasoningOption struct {
	Type   string   `json:"type"`
	Values []string `json:"values"`
}

// Catalog is a cached, queryable view of the models.dev catalog.
type Catalog struct {
	mu       sync.RWMutex
	client   *http.Client
	url      string
	fetched  time.Time
	byID     map[string]*ModelMetadata // keyed by full ID lowercased (e.g. "openai/gpt-5.5")
	byBareID map[string]*ModelMetadata // keyed by bare ID lowercased (e.g. "gpt-5.5")
	byName   map[string]*ModelMetadata // keyed by display name lowercased (e.g. "gpt-5.5")
	loaded   bool
}

// New creates a catalog fetcher. The HTTP client defaults to a 30s timeout
// if nil.
func New(client *http.Client) *Catalog {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Catalog{
		client:   client,
		url:      CatalogURL,
		byID:     make(map[string]*ModelMetadata),
		byBareID: make(map[string]*ModelMetadata),
		byName:   make(map[string]*ModelMetadata),
	}
}

// Lookup finds metadata for a model by its ID. Matching is universal —
// it does not depend on the provider prefix matching models.dev's prefix.
// Order:
//  1. Exact full-ID match (case-insensitive)
//  2. Provider-hint-prefixed match (e.g. hint="openai", id="gpt-5.5")
//  3. Bare-ID match: strip any provider prefix from the query, then
//     match against catalog bare IDs (e.g. "tokenrouter/gemini-3.7-flash"
//     → "gemini-3.7-flash" → matches catalog's "google/gemini-3.7-flash")
//  4. Display-name match (case-insensitive)
//
// This ensures models from any gateway (OpenRouter, TokenRouter, OmniRoute,
// etc.) get enriched as long as the model name itself exists in the catalog.
func (c *Catalog) Lookup(providerHint, modelID string) *ModelMetadata {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.loaded || modelID == "" {
		return nil
	}
	lower := strings.ToLower(modelID)
	// 1. Exact full-ID match
	if m, ok := c.byID[lower]; ok {
		return m
	}
	// 2. Provider hint prefix
	if providerHint != "" {
		prefixed := strings.ToLower(providerHint) + "/" + lower
		if m, ok := c.byID[prefixed]; ok {
			return m
		}
	}
	// 3. Bare-ID match — strip prefix from query side too
	bare := lower
	if idx := strings.Index(bare, "/"); idx >= 0 {
		bare = bare[idx+1:]
	}
	if bare != lower {
		if m, ok := c.byBareID[bare]; ok {
			return m
		}
	}
	// Also try the original as bare (in case catalog has it without prefix)
	if m, ok := c.byBareID[lower]; ok {
		return m
	}
	if m, ok := c.byBareID[bare]; ok {
		return m
	}
	// 4. Display-name match
	if m, ok := c.byName[lower]; ok {
		return m
	}
	if m, ok := c.byName[bare]; ok {
		return m
	}
	// 5. Provider-suffix fallback — gateways like OpenRouter append ":free"
	// or "-free" to model IDs to denote free-tier variants. The base model
	// metadata (reasoning efforts, context, pricing) is the same, so strip
	// the suffix and retry the full lookup chain.
	if stripped := stripProviderSuffix(lower); stripped != lower && stripped != "" {
		if m := c.lookupUnlocked(providerHint, stripped); m != nil {
			return m
		}
	}
	return nil
}

// lookupUnlocked performs the same lookup chain as Lookup without
// re-acquiring the read lock. Used internally by Lookup after stripping
// provider-added suffixes.
func (c *Catalog) lookupUnlocked(providerHint, modelID string) *ModelMetadata {
	lower := strings.ToLower(modelID)
	if m, ok := c.byID[lower]; ok {
		return m
	}
	if providerHint != "" {
		prefixed := strings.ToLower(providerHint) + "/" + lower
		if m, ok := c.byID[prefixed]; ok {
			return m
		}
	}
	bare := lower
	if idx := strings.Index(bare, "/"); idx >= 0 {
		bare = bare[idx+1:]
	}
	if bare != lower {
		if m, ok := c.byBareID[bare]; ok {
			return m
		}
	}
	if m, ok := c.byBareID[lower]; ok {
		return m
	}
	if m, ok := c.byBareID[bare]; ok {
		return m
	}
	if m, ok := c.byName[lower]; ok {
		return m
	}
	if m, ok := c.byName[bare]; ok {
		return m
	}
	return nil
}

// stripProviderSuffix removes provider-added suffixes that denote free-tier
// or variant models. Gateways like OpenRouter append ":free" or "-free" to
// model IDs (e.g. "qwen/qwen3.8-max:free" → "qwen/qwen3.8-max"). The base
// model metadata is the same. Returns the original string if no known
// suffix was found.
func stripProviderSuffix(id string) string {
	for _, suffix := range []string{":free", "-free", ":nitro", "-nitro"} {
		if strings.HasSuffix(id, suffix) {
			return strings.TrimSuffix(id, suffix)
		}
	}
	return id
}

// ensureLoaded fetches the catalog if stale or not yet loaded.
func (c *Catalog) ensureLoaded(ctx context.Context) error {
	c.mu.RLock()
	fresh := c.loaded && time.Since(c.fetched) < CacheTTL
	c.mu.RUnlock()
	if fresh {
		return nil
	}
	return c.fetch(ctx)
}

// fetch downloads and indexes the catalog. If the live fetch fails
// (upstream down, offline, timeout), it falls back to the embedded
// catalog generated at build time by cmd/gen-catalog.
func (c *Catalog) fetch(ctx context.Context) error {
	entries, err := c.fetchLive(ctx)
	if err != nil {
		// Fallback to embedded catalog
		embedded, embErr := parseEmbeddedCatalog()
		if embErr != nil {
			return fmt.Errorf("modelcatalog: live fetch failed (%v) and embedded fallback failed (%v)", err, embErr)
		}
		c.indexEntries(embedded)
		return nil
	}
	c.indexEntries(entries)
	return nil
}

// fetchLive downloads the catalog from the live URL and converts to
// flat entry list.
func (c *Catalog) fetchLive(ctx context.Context) ([]flatEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch returned %d", resp.StatusCode)
	}
	var providers map[string]catalogProvider
	if err := json.NewDecoder(resp.Body).Decode(&providers); err != nil {
		return nil, fmt.Errorf("parse failed: %w", err)
	}
	entries := make([]flatEntry, 0, 500)
	for _, prov := range providers {
		for modelKey, cm := range prov.Models {
			entries = append(entries, flatEntry{
				ID:       modelKey,
				Metadata: convertModel(modelKey, cm),
			})
		}
	}
	return entries, nil
}

// flatEntry is a model ID + its metadata, used by both live and
// embedded catalog paths to build the index.
type flatEntry struct {
	ID       string
	Metadata *ModelMetadata
}

// parseEmbeddedCatalog parses the build-time embedded JSON constant
// into flat entries.
func parseEmbeddedCatalog() ([]flatEntry, error) {
	var entries []struct {
		ID               string   `json:"id"`
		Name             string   `json:"name"`
		Description      string   `json:"description"`
		Context          int      `json:"context"`
		Output           int      `json:"output"`
		InputCost        float64  `json:"input_cost"`
		OutputCost       float64  `json:"output_cost"`
		CacheReadCost    float64  `json:"cache_read_cost"`
		Reasoning        bool     `json:"reasoning"`
		ToolCall         bool     `json:"tool_call"`
		StructuredOutput bool     `json:"structured_output"`
		Vision           bool     `json:"vision"`
		SupportedEfforts []string `json:"supported_efforts"`
		KnowledgeCutoff  string   `json:"knowledge_cutoff"`
		Kind             string   `json:"kind"`
	}
	if err := json.Unmarshal([]byte(config.EmbeddedCatalogJSON), &entries); err != nil {
		return nil, fmt.Errorf("embedded parse: %w", err)
	}
	out := make([]flatEntry, len(entries))
	for i, e := range entries {
		efforts := e.SupportedEfforts
		if e.Reasoning && len(efforts) == 0 {
			efforts = defaultReasoningEfforts
		}
		out[i] = flatEntry{
			ID: e.ID,
			Metadata: &ModelMetadata{
				ID:               e.ID,
				Name:             e.Name,
				Description:      e.Description,
				Context:          e.Context,
				Output:           e.Output,
				InputCost:        e.InputCost,
				OutputCost:       e.OutputCost,
				CacheReadCost:    e.CacheReadCost,
				Reasoning:        e.Reasoning,
				ToolCall:         e.ToolCall,
				StructuredOutput: e.StructuredOutput,
				Vision:           e.Vision,
				SupportedEfforts: efforts,
				KnowledgeCutoff:  e.KnowledgeCutoff,
				Kind:             e.Kind,
			},
		}
	}
	return out, nil
}

// indexEntries builds the flat lookup index from a list of entries.
func (c *Catalog) indexEntries(entries []flatEntry) {
	byID := make(map[string]*ModelMetadata, len(entries))
	byBareID := make(map[string]*ModelMetadata, len(entries))
	byName := make(map[string]*ModelMetadata, len(entries))
	for _, e := range entries {
		meta := e.Metadata
		lowerKey := strings.ToLower(e.ID)
		byID[lowerKey] = meta
		bareID := lowerKey
		if idx := strings.Index(bareID, "/"); idx >= 0 {
			bareID = bareID[idx+1:]
		}
		if _, exists := byBareID[bareID]; !exists {
			byBareID[bareID] = meta
		}
		if meta.Name != "" {
			lowerName := strings.ToLower(meta.Name)
			if _, exists := byName[lowerName]; !exists {
				byName[lowerName] = meta
			}
		}
	}
	c.mu.Lock()
	c.byID = byID
	c.byBareID = byBareID
	c.byName = byName
	c.fetched = time.Now()
	c.loaded = true
	c.mu.Unlock()
}

// convertModel maps a catalogModel to ModelMetadata.
func convertModel(id string, cm catalogModel) *ModelMetadata {
	meta := &ModelMetadata{
		ID:               id,
		Name:             cm.Name,
		Description:      cm.Description,
		Context:          cm.Limit.Context,
		Output:           cm.Limit.Output,
		InputCost:        cm.Cost.Input,
		OutputCost:       cm.Cost.Output,
		CacheReadCost:    cm.Cost.CacheRead,
		Reasoning:        cm.Reasoning,
		ToolCall:         cm.ToolCall,
		StructuredOutput: cm.StructuredOutput,
		Temperature:      cm.Temperature,
		KnowledgeCutoff:  cm.Knowledge,
		ReleaseDate:      cm.ReleaseDate,
		Kind:             detectKind(id, cm),
	}
	// Vision: from modalities.input includes "image" or attachment=true
	for _, m := range cm.Modalities.Input {
		if m == "image" {
			meta.Vision = true
			break
		}
	}
	if cm.Attachment {
		meta.Vision = true
	}
	// Audio: from modalities.input includes "audio"
	for _, m := range cm.Modalities.Input {
		if m == "audio" {
			meta.Audio = true
			break
		}
	}
	// Video: from modalities.input includes "video"
	for _, m := range cm.Modalities.Input {
		if m == "video" {
			meta.Video = true
			break
		}
	}
	// Supported efforts from reasoning_options
	for _, opt := range cm.ReasoningOptions {
		if opt.Type == "effort" {
			meta.SupportedEfforts = append(meta.SupportedEfforts, opt.Values...)
		}
	}
	// When the catalog marks a model as reasoning=true but doesn't list
	// effort levels (models.dev leaves reasoning_options=[] for many
	// reasoners), fill in a default effort set so the UI shows the
	// selector. "auto" (the default selection) still omits
	// reasoning_effort on the wire, so this only affects UI visibility.
	if meta.Reasoning && len(meta.SupportedEfforts) == 0 {
		meta.SupportedEfforts = defaultReasoningEfforts
	}
	return meta
}

// defaultReasoningEfforts is the fallback effort set for reasoners whose
// catalog entry doesn't list specific effort levels. Matches the common
// set supported by most OpenAI-compatible reasoning models.
var defaultReasoningEfforts = []string{"low", "medium", "high"}

// embeddingNamePatterns matches model IDs/names that indicate an embedding
// model. These produce vectors, not text — they must never appear in the
// chat model picker.
var embeddingNamePatterns = []string{
	"embed", "embedding", "bge-", "e5-", "gte-", "jina-embed",
	"nomic-embed", "voyage-", "text-embedding", "nv-embed",
	"nemoretriever", "embedcode",
}

// detectKind categorizes a model by what it produces, using (in order):
//  1. Name pattern (embedding models are name-based — no modality signal)
//  2. Output modality (image/video/audio output = generation model)
//  3. Input modality (audio input + text output = STT)
//  4. Default: chat (text input → text output)
//
// Description is used as a tiebreaker for ambiguous cases.
func detectKind(id string, cm catalogModel) string {
	lowerID := strings.ToLower(id)
	lowerName := strings.ToLower(cm.Name)
	lowerDesc := strings.ToLower(cm.Description)

	// 1. Embedding: name-based detection (no modality signal — embeddings
	// report text→text like chat models)
	for _, p := range embeddingNamePatterns {
		if strings.Contains(lowerID, p) || strings.Contains(lowerName, p) {
			return "embedding"
		}
	}
	if strings.Contains(lowerDesc, "embedding model") {
		return "embedding"
	}

	// Image generators by well-known id, even when modalities are missing.
	if strings.Contains(lowerID, "gpt-image") || strings.Contains(lowerName, "gpt-image") ||
		strings.Contains(lowerID, "dall-e") || strings.Contains(lowerName, "dall-e") {
		return "image"
	}

	outMod := cm.Modalities.Output
	inMod := cm.Modalities.Input

	// 2. Generation models: non-text output
	hasImageOut := contains(outMod, "image")
	hasVideoOut := contains(outMod, "video")
	hasAudioOut := contains(outMod, "audio")
	hasTextOut := contains(outMod, "text")

	switch {
	case hasVideoOut:
		return "video"
	case hasImageOut && !hasTextOut:
		return "image"
	case hasAudioOut && !hasTextOut:
		return "tts"
	}

	// 3. STT: audio input → text output (but not image input, which is
	// a multimodal LLM with audio capability)
	hasAudioIn := contains(inMod, "audio")
	hasImageIn := contains(inMod, "image")
	if hasAudioIn && hasTextOut && !hasImageIn {
		return "stt"
	}

	// 4. Default: chat LLM (text or text+image input → text output)
	return "chat"
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// EnrichAll enriches a batch of model IDs with catalog metadata.
// Call ensureLoaded first. Returns the metadata for each ID (nil if not found).
func (c *Catalog) EnrichAll(providerHint string, modelIDs []string) []*ModelMetadata {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.loaded {
		return nil
	}
	out := make([]*ModelMetadata, len(modelIDs))
	for i, id := range modelIDs {
		out[i] = c.Lookup(providerHint, id)
	}
	return out
}

// Refresh forces a re-fetch on next Lookup.
func (c *Catalog) Refresh() {
	c.mu.Lock()
	c.loaded = false
	c.mu.Unlock()
}

// SetURL overrides the live catalog URL (for testing/simulation).
func (c *Catalog) SetURL(url string) {
	c.mu.Lock()
	c.url = url
	c.loaded = false
	c.mu.Unlock()
}

// Stats returns the number of models indexed.
func (c *Catalog) Stats() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.byID)
}

// EnsureLoaded is the public version for external callers.
func (c *Catalog) EnsureLoaded(ctx context.Context) error {
	return c.ensureLoaded(ctx)
}

// Loaded reports whether the catalog has been fetched and indexed.
func (c *Catalog) Loaded() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.loaded
}
