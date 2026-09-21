package modelcatalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCatalogEnsureLoadedIndexesLiveCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"openai": {
				"id": "openai",
				"name": "OpenAI",
				"models": {
					"openai/gpt-test": {
						"name": "GPT Test",
						"description": "A reasoning model",
						"reasoning": true,
						"tool_call": true,
						"structured_output": true,
						"temperature": true,
						"attachment": true,
						"knowledge": "2026-01-01",
						"release_date": "2026-02-01",
						"modalities": {"input": ["text", "image"], "output": ["text"]},
						"limit": {"context": 128000, "output": 16000},
						"cost": {"input": 1.5, "output": 6, "cache_read": 0.2},
						"reasoning_options": [{"type": "effort", "values": ["low", "high"]}],
						"interleaved": {"field": " Reasoning_Content "}
					}
				}
			}
		}`))
	}))
	defer server.Close()

	catalog := New(server.Client())
	catalog.url = server.URL
	if err := catalog.EnsureLoaded(context.Background()); err != nil {
		t.Fatalf("EnsureLoaded failed: %v", err)
	}
	if !catalog.Loaded() || catalog.size != 1 {
		t.Fatalf("catalog state = loaded:%t stats:%d, want loaded:true stats:1", catalog.Loaded(), catalog.size)
	}

	meta := catalog.Lookup("openai", "gpt-test")
	if meta == nil {
		t.Fatal("Lookup did not find model through provider hint")
	}
	if meta.Context != 128000 || meta.Output != 16000 || meta.InputCost != 1.5 || meta.OutputCost != 6 || meta.CacheReadCost != 0.2 {
		t.Fatalf("metadata limits/costs = %#v, want values from live catalog", meta)
	}
	if !meta.Reasoning || !meta.ToolCall || !meta.StructuredOutput || !meta.Temperature || !meta.Vision {
		t.Fatalf("metadata capabilities = %#v, want reasoning/tool/structured/temperature/vision", meta)
	}
	if meta.InterleavedField != "reasoning_content" || len(meta.SupportedEfforts) != 2 {
		t.Fatalf("metadata reasoning fields = %#v, want normalized interleaved and efforts", meta)
	}

	if got := catalog.Lookup("", "gateway/gpt-test"); got != meta {
		t.Fatalf("bare lookup = %#v, want same metadata pointer", got)
	}
	if got := catalog.Lookup("", "GPT Test"); got != meta {
		t.Fatalf("display-name lookup = %#v, want same metadata pointer", got)
	}
	if got := catalog.Lookup("", "gpt-test"); got != meta {
		t.Fatalf("loaded-model lookup = %#v, want same metadata pointer", got)
	}
	if got := catalog.Lookup("", "missing"); got != nil {
		t.Fatalf("missing-model lookup = %#v, want nil", got)
	}
}

func TestCatalogLookupPrefersConfiguredGatewayNamespace(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"opencode": {
				"id": "opencode",
				"name": "OpenCode",
				"models": {
					"deepseek-v4.1-flash": {
						"name": "DeepSeek V4.1 Flash",
						"modalities": {"input": ["text", "image"], "output": ["text"]},
						"limit": {"context": 1000000, "output": 128000}
					}
				}
			},
			"openrouter": {
				"id": "openrouter",
				"name": "OpenRouter",
				"models": {
					"deepseek/deepseek-v4.1-flash": {
						"name": "DeepSeek V4.1 Flash",
						"modalities": {"input": ["text"], "output": ["text"]},
						"limit": {"context": 200000, "output": 64000}
					}
				}
			}
		}`))
	}))
	defer server.Close()

	catalog := New(server.Client())
	catalog.url = server.URL
	if err := catalog.EnsureLoaded(context.Background()); err != nil {
		t.Fatalf("EnsureLoaded failed: %v", err)
	}

	opencode := catalog.Lookup("opencode", "deepseek-v4.1-flash")
	if opencode == nil || opencode.Context != 1000000 || !opencode.Vision {
		t.Fatalf("opencode bare lookup = %#v, want OpenCode vision model", opencode)
	}
	opencode = catalog.Lookup("opencode", "deepseek/deepseek-v4.1-flash")
	if opencode == nil || opencode.Context != 1000000 || !opencode.Vision {
		t.Fatalf("opencode vendor-prefixed lookup = %#v, want OpenCode vision model", opencode)
	}
	openrouter := catalog.Lookup("openrouter", "deepseek/deepseek-v4.1-flash")
	if openrouter == nil || openrouter.Context != 200000 || openrouter.Vision {
		t.Fatalf("openrouter lookup = %#v, want OpenRouter vendor-qualified model", openrouter)
	}
}

func TestCatalogRefreshFetchesOnlyWhenStale(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"provider":{"models":{"provider/model":{"name":"Model"}}}}`))
	}))
	defer server.Close()

	catalog := New(server.Client())
	catalog.url = server.URL
	ctx := context.Background()
	if err := catalog.EnsureLoaded(ctx); err != nil {
		t.Fatalf("first EnsureLoaded failed: %v", err)
	}
	if err := catalog.EnsureLoaded(ctx); err != nil {
		t.Fatalf("fresh EnsureLoaded failed: %v", err)
	}
	if requests != 1 {
		t.Fatalf("fresh catalog requests = %d, want 1", requests)
	}
	catalog.loaded = false
	if catalog.Loaded() {
		t.Fatal("catalog still marked loaded after reset")
	}
	if err := catalog.EnsureLoaded(ctx); err != nil {
		t.Fatalf("refreshed EnsureLoaded failed: %v", err)
	}
	if requests != 2 {
		t.Fatalf("refreshed catalog requests = %d, want 2", requests)
	}
}

func TestCatalogFallsBackToEmbeddedCatalogWhenLiveFetchFails(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("offline")
	})}
	catalog := New(client)
	catalog.url = "https://catalog.invalid/api.json"

	if err := catalog.EnsureLoaded(context.Background()); err != nil {
		t.Fatalf("EnsureLoaded fallback failed: %v", err)
	}
	if !catalog.Loaded() || catalog.size == 0 {
		t.Fatalf("fallback state = loaded:%t stats:%d, want loaded catalog with entries", catalog.Loaded(), catalog.size)
	}
	opencode := catalog.Lookup("opencode", "deepseek/deepseek-v4.1-flash")
	if opencode == nil || opencode.Context != 1_000_000 || !opencode.Vision {
		t.Fatalf("embedded opencode lookup = %#v, want OpenCode DeepSeek vision entry", opencode)
	}
	openrouter := catalog.Lookup("openrouter", "deepseek/deepseek-v4.1-flash")
	if openrouter == nil || openrouter.Context != 1_048_576 || !openrouter.Vision {
		t.Fatalf("embedded openrouter lookup = %#v, want OpenRouter DeepSeek entry", openrouter)
	}
}

func TestCatalogLookupReturnsNilUntilLoaded(t *testing.T) {
	catalog := New(nil)
	if got := catalog.Lookup("", "anything"); got != nil {
		t.Fatalf("unloaded Lookup = %#v, want nil", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
