package catalog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

func newProvider(t *testing.T, endpoint string, style enum.Style) *aggregate.Provider {
	t.Helper()
	g, err := aggregate.NewProvider("g", "G", endpoint, style)
	if err != nil {
		t.Fatal(err)
	}
	return &g
}

// The OpenRouter-shaped payload carries the metadata the record imports:
// limits, modalities, effort levels, identity (canonical/alias), and
// per-token USD pricing normalized to the per-1M rate card.
func TestListModelsCompletionMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Error("api key must ride as a bearer token")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":             "z-ai/glm-5.2",
					"name":           "Z.ai: GLM 5.2",
					"canonical_slug": "z-ai/glm-5.2-20260301",
					"context_length": 1000000,
					"architecture": map[string]any{
						"input_modalities":  []string{"text", "image"},
						"output_modalities": []string{"text"},
					},
					"top_provider": map[string]any{"context_length": 1000000, "max_completion_tokens": 131072},
					"reasoning":    map[string]any{"supported_efforts": []string{"max", "high", "low"}},
					"pricing":      map[string]any{"prompt": "0.0000028", "completion": "0.0000088", "input_cache_read": "0.00000056"},
				},
				{
					"id":             "z-ai/glm-5.2:free",
					"context_length": 200_000, // variant entries carry their own limits
				},
				{
					"id":           "~z-ai/glm-latest",
					"alias_target": map[string]any{"name": "Z.ai: GLM", "slug": "z-ai/glm-5.2"},
				},
				{
					"id":      "bad/pricing",
					"pricing": map[string]any{"prompt": "not-a-number"},
				},
			},
		})
	}))
	defer srv.Close()

	g := newProvider(t, srv.URL, enum.StyleCompletion)
	g.SetApiKey("k")
	models, err := New(nil).ListModels(t.Context(), g)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 4 {
		t.Fatalf("models = %+v", models)
	}
	m := models[0]
	if m.Name != "Z.ai: GLM 5.2" || m.Canonical != "z-ai/glm-5.2-20260301" ||
		m.ContextWindow != 1_000_000 || m.OutputLimit != 131_072 {
		t.Fatalf("identity/limits mismatch: %+v", m)
	}
	if len(m.InputModalities) != 2 || m.InputModalities[1] != enum.ModalityImage ||
		len(m.OutputModalities) != 1 || m.OutputModalities[0] != enum.ModalityText {
		t.Fatalf("modalities mismatch: %+v", m)
	}
	if len(m.SupportedEfforts) != 3 || m.SupportedEfforts[0] != enum.EffortMax {
		t.Fatalf("efforts mismatch: %+v", m)
	}
	if m.Pricing == nil || m.Pricing.InputPerM() != 2.8 || m.Pricing.OutputPerM() != 8.8 ||
		m.Pricing.CacheReadPerM() != 0.56 || m.Pricing.CacheWritePerM() != 0 {
		t.Fatalf("pricing must convert per-token to per-1M: %+v", m.Pricing)
	}
	free := models[1]
	if free.ContextWindow != 200_000 || free.OutputLimit != 0 {
		t.Fatalf("variant entry keeps its own limits: %+v", free)
	}
	alias := models[2]
	if alias.AliasOf != "z-ai/glm-5.2" {
		t.Fatalf("alias target lost: %+v", alias)
	}
	if models[3].Pricing != nil {
		t.Fatal("malformed pricing must drop, not zero-fill")
	}
}

// opencode's zen/go catalogs use the models.dev shape: a model kind,
// MIME-typed modality lists, token limits, and reasoning efforts under
// capabilities. The kind and modalities are what keep decision, image,
// realtime, and embedding entries out of the chat picker.
func TestListModelsModelsDevShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []map[string]any{
			{
				"id": "deepseek-v4.1-flash", "name": "DeepSeek V4.1 Flash", "type": "chat", "recommended_api": "chat-completions",
				"canonical_id": "deepseek/deepseek-v4.1-flash-1017",
				"modalities":   map[string]any{"input": []string{"text", "image/jpeg", "image/png", "application/pdf"}, "output": []string{"text"}},
				"limit":        map[string]any{"context": 1000000, "output": 384000},
				"capabilities": map[string]any{"reasoning": map[string]any{"supported": true, "effort": []string{"low", "high", "max"}}},
			},
			{"id": "gemini-decision-1", "type": "decision", "modalities": map[string]any{"input": []string{"text"}, "output": []string{"decision"}}},
			{"id": "gpt-image-2.5", "type": "image", "modalities": map[string]any{"input": []string{"text"}, "output": []string{"image/png"}}},
			{"id": "gpt-live-1", "type": "realtime", "modalities": map[string]any{"input": []string{"text", "audio/pcm"}, "output": []string{"text", "audio/pcm"}}},
			{"id": "embed-untyped", "modalities": map[string]any{"input": []string{"text"}, "output": []string{"embedding"}}},
			{"id": "gpt-6-luna", "type": "chat", "recommended_api": "responses"},
			{"id": "claude-haiku-5-5", "type": "chat", "recommended_api": "messages"},
			{"id": "gemini-4-argon", "type": "chat", "recommended_api": "interactions"},
		}})
	}))
	defer srv.Close()

	models, err := New(nil).ListModels(t.Context(), newProvider(t, srv.URL, enum.StyleCompletion))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 8 {
		t.Fatalf("models = %+v", models)
	}
	// recommended_api pins the wire style only for APIs the provider's own
	// base URL serves; Google surfaces stay unpinned.
	for i, want := range []enum.Style{enum.StyleCompletion, enum.StyleResponses, enum.StyleMessages, ""} {
		idx := []int{0, 5, 6, 7}[i]
		if got := models[idx].Style; got != want {
			t.Fatalf("%s style = %q, want %q", models[idx].ID, got, want)
		}
	}
	chat := models[0]
	if chat.ModelType != enum.ModelTypeText || chat.Canonical != "deepseek/deepseek-v4.1-flash-1017" ||
		chat.ContextWindow != 1_000_000 || chat.OutputLimit != 384_000 {
		t.Fatalf("chat entry mismatch: %+v", chat)
	}
	if got := chat.InputModalities; len(got) != 3 || got[0] != enum.ModalityText || got[1] != enum.ModalityImage || got[2] != enum.ModalityFile {
		t.Fatalf("MIME modalities must collapse to families: %+v", got)
	}
	if len(chat.SupportedEfforts) != 3 || chat.SupportedEfforts[2] != enum.EffortMax {
		t.Fatalf("efforts from capabilities.reasoning: %+v", chat.SupportedEfforts)
	}
	for i, want := range []enum.ModelType{enum.ModelTypeDecision, enum.ModelTypeImage, enum.ModelTypeRealtime, enum.ModelTypeEmbedding} {
		if got := models[i+1].ModelType; got != want {
			t.Fatalf("%s type = %q, want %q", models[i+1].ID, got, want)
		}
	}
	if out := models[2].OutputModalities; len(out) != 1 || out[0] != enum.ModalityImage {
		t.Fatalf("image output modalities: %+v", out)
	}
}

// Anthropic's /v1/models declares only id + display_name — everything
// else stays zero so the merge reads "the provider did not say".
func TestListModelsMessagesThinMetadata(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "k" || r.Header.Get("anthropic-version") == "" {
			t.Error("messages-style headers missing")
		}
		pages = append(pages, r.URL.Query().Get("after_id"))
		if len(pages) == 1 {
			json.NewEncoder(w).Encode(map[string]any{
				"data":     []map[string]any{{"id": "claude-a", "display_name": "Claude A"}},
				"has_more": true, "last_id": "claude-a",
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "claude-b", "display_name": "Claude B"}},
		})
	}))
	defer srv.Close()

	g := newProvider(t, srv.URL, enum.StyleMessages)
	g.SetApiKey("k")
	models, err := New(nil).ListModels(t.Context(), g)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "claude-a" || models[0].Name != "Claude A" ||
		models[1].ID != "claude-b" {
		t.Fatalf("models = %+v", models)
	}
	if models[0].ContextWindow != 0 || len(models[0].SupportedEfforts) != 0 {
		t.Fatalf("thin catalog must stay zero: %+v", models[0])
	}
}

// Gemini's models[] carries token limits under its own field names and
// filters out entries without generateContent.
func TestListModelsGenerateContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "k" {
			t.Error("generatecontent-style key header missing")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{
					"name": "models/gemini-3-pro", "displayName": "Gemini 3 Pro",
					"inputTokenLimit": 1_048_576, "outputTokenLimit": 65_536,
					"supportedGenerationMethods": []string{"generateContent"},
				},
				{
					"name":                       "models/embedding-1",
					"supportedGenerationMethods": []string{"embedContent"},
				},
			},
		})
	}))
	defer srv.Close()

	g := newProvider(t, srv.URL, enum.StyleGenerateContent)
	g.SetApiKey("k")
	models, err := New(nil).ListModels(t.Context(), g)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "gemini-3-pro" {
		t.Fatalf("models = %+v", models)
	}
	if models[0].Name != "Gemini 3 Pro" || models[0].ContextWindow != 1_048_576 ||
		models[0].OutputLimit != 65_536 {
		t.Fatalf("gemini limits mismatch: %+v", models[0])
	}
}

// Provider discovery places the model id in the URL path. An id
// containing a query delimiter must not rewrite the request: the
// `/endpoints` suffix otherwise lands in the query and the provider
// fetches the wrong resource. Escape the id or reject it before the
// request; never interpolate raw URL syntax.
func TestListUpstreamsEscapesModelIDPath(t *testing.T) {
	var rawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"endpoints": []map[string]any{{"provider_name": "Alpha"}}},
		})
	}))
	defer srv.Close()
	g := newProvider(t, srv.URL+"/openrouter.ai", enum.StyleCompletion)
	g.SetApiKey("k")
	if _, err := New(nil).ListUpstreams(t.Context(), g, "vendor/model?x=1"); err != nil {
		return // rejecting the unsafe id is safe
	}
	if rawQuery != "" {
		t.Fatalf("model id leaked into provider-discovery query: %q", rawQuery)
	}
}

// Provider discovery puts a model id's path segments into the outbound
// URL. PathEscape deliberately leaves "." and ".." alone, so a catalog
// id with a parent segment can climb out of /models/ and fetch a
// different provider resource. Reject or escape traversal segments
// before issuing the request.
func TestListUpstreamsRejectsTraversalModelID(t *testing.T) {
	var seenPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.EscapedPath()
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"endpoints": []map[string]any{{"provider_name": "Alpha"}}},
		})
	}))
	defer srv.Close()
	g := newProvider(t, srv.URL+"/openrouter.ai", enum.StyleCompletion)
	g.SetApiKey("k")
	if _, err := New(nil).ListUpstreams(t.Context(), g, "../admin"); err != nil {
		return // rejecting the unsafe id is safe
	}
	if strings.Contains(seenPath, "..") {
		t.Fatalf("model id escaped the provider model path: %q", seenPath)
	}
}

// A catalog with no kind field still says what a model makes: the media it
// outputs. A model that makes images, video, or sound is not a chat model even
// when it also answers in text.
func TestListModelsKindFromOutputModalities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arch := func(in, out []string) map[string]any {
			return map[string]any{"architecture": map[string]any{"input_modalities": in, "output_modalities": out}}
		}
		entry := func(id string, in, out []string) map[string]any {
			e := arch(in, out)
			e["id"] = id
			return e
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			entry("vendor/chat", []string{"text", "image"}, []string{"text"}),
			entry("vendor/picture", []string{"image", "text"}, []string{"image", "text"}),
			entry("vendor/clip", []string{"text", "image", "audio"}, []string{"video"}),
			entry("vendor/voice", []string{"text"}, []string{"text", "audio"}),
			{"id": "vendor/plain"},
		}})
	}))
	defer srv.Close()
	models, err := New(nil).ListModels(t.Context(), newProvider(t, srv.URL, enum.StyleCompletion))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]enum.ModelType{
		"vendor/chat": "", "vendor/picture": enum.ModelTypeImage, "vendor/clip": enum.ModelTypeVideo,
		"vendor/voice": enum.ModelTypeSpeech, "vendor/plain": "",
	}
	for _, m := range models {
		if m.ModelType != want[m.ID] {
			t.Errorf("%s type = %q, want %q", m.ID, m.ModelType, want[m.ID])
		}
	}
}

// With no kind and no modalities (OpenAI and Ollama list that little), the
// name is what there is to go on.
func TestTypeFromID(t *testing.T) {
	for id, want := range map[string]enum.ModelType{
		"text-embedding-3-small":      enum.ModelTypeEmbedding,
		"nomic-embed-text:latest":     enum.ModelTypeEmbedding,
		"gemini-embedding-001":        enum.ModelTypeEmbedding,
		"mxbai-embed-large":           enum.ModelTypeEmbedding,
		"BAAI/bge-reranker-v2":        enum.ModelTypeRerank,
		"rerank-v3.5":                 enum.ModelTypeRerank,
		"whisper-1":                   enum.ModelTypeTranscription,
		"gpt-4o-transcribe":           enum.ModelTypeTranscription,
		"tts-1-hd":                    enum.ModelTypeSpeech,
		"gpt-4o-mini-tts":             enum.ModelTypeSpeech,
		"dall-e-3":                    enum.ModelTypeImage,
		"gpt-image-1":                 enum.ModelTypeImage,
		"black-forest-labs/flux-pro":  enum.ModelTypeImage,
		"sora-2":                      enum.ModelTypeVideo,
		"veo-3.1":                     enum.ModelTypeVideo,
		"gpt-4o":                      "",
		"llama3.1:8b":                 "",
		"embedder-chat":               "",
		"claude-sonnet-5-5":           "",
		"deepseek-v4.1-flash":         "",
		"qwen3-coder-next:cloud":      "",
		"mistral-small-latest":        "",
		"gpt-4o-audio-preview":        "",
		"vendor/stt-but-chat-no":      enum.ModelTypeTranscription, // a word of the id is enough
		"o4-mini-deep-research":       "",
		"text-moderation-latest":      "",
		"grok-4-fast-non-reasoning":   "",
		"gemini-2.5-flash-native-tts": enum.ModelTypeSpeech,
	} {
		if got := typeFromID(id); got != want {
			t.Errorf("typeFromID(%q) = %q, want %q", id, got, want)
		}
	}
	// An undeclared catalog entry takes the kind from its id; a declared one
	// does not.
	if got := (catalogEntry{ID: "text-embedding-3-large"}).toCatalogModel().ModelType; got != enum.ModelTypeEmbedding {
		t.Errorf("undeclared embedding model typed %q", got)
	}
	if got := (catalogEntry{ID: "text-embedding-3-large", Type: "chat"}).toCatalogModel().ModelType; got != enum.ModelTypeText {
		t.Errorf("a declared kind must win, got %q", got)
	}
}

// reroute sends every request to one test server, whatever host it names.
type reroute struct{ to *url.URL }

func (r reroute) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = r.to.Scheme, r.to.Host
	return http.DefaultTransport.RoundTrip(req)
}

// OpenRouter leaves every model that does not answer in text out of /models
// unless asked for all outputs, and names the kind of the rest as their
// output: "speech", "transcription", "embeddings", "decisions", "rerank".
func TestListModelsOpenRouterAsksForEveryOutput(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		entry := func(id string, in, out []string) map[string]any {
			return map[string]any{"id": id, "architecture": map[string]any{"input_modalities": in, "output_modalities": out}}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			entry("openai/gpt-5", []string{"text"}, []string{"text"}),
			entry("recraft/recraft-v4.1-flash-v2:free", []string{"text"}, []string{"image"}),
			entry("heygen/heygen-voice-1-turbo:free", []string{"text"}, []string{"speech"}),
			entry("openai/whisper-1", []string{"audio"}, []string{"transcription"}),
			entry("cohere/embed-5-fast", []string{"text", "image"}, []string{"embeddings"}),
			entry("heygen/heygen-decision-1", []string{"text"}, []string{"decisions"}),
			entry("cohere/rerank-4", []string{"text"}, []string{"rerank"}),
			entry("heygen/heygen-video-1", []string{"text", "image"}, []string{"video"}),
		}})
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)
	c := New(&http.Client{Transport: reroute{target}})

	models, err := c.ListModels(t.Context(), newProvider(t, "https://openrouter.ai/api/v1", enum.StyleCompletion))
	if err != nil {
		t.Fatal(err)
	}
	if query != "output_modalities=all" {
		t.Fatalf("query = %q, want output_modalities=all", query)
	}
	want := map[string]enum.ModelType{
		"openai/gpt-5": "", "recraft/recraft-v4.1-flash-v2:free": enum.ModelTypeImage,
		"heygen/heygen-voice-1-turbo:free": enum.ModelTypeSpeech, "openai/whisper-1": enum.ModelTypeTranscription,
		"cohere/embed-5-fast": enum.ModelTypeEmbedding, "heygen/heygen-decision-1": enum.ModelTypeDecision,
		"cohere/rerank-4": enum.ModelTypeRerank, "heygen/heygen-video-1": enum.ModelTypeVideo,
	}
	if len(models) != len(want) {
		t.Fatalf("models = %+v", models)
	}
	for _, m := range models {
		if m.ModelType != want[m.ID] {
			t.Errorf("%s type = %q, want %q", m.ID, m.ModelType, want[m.ID])
		}
	}

	// Another provider is not asked for what only OpenRouter understands.
	query = "unset"
	if _, err := New(nil).ListModels(t.Context(), newProvider(t, srv.URL, enum.StyleCompletion)); err != nil {
		t.Fatal(err)
	}
	if query != "" {
		t.Fatalf("a provider that is not OpenRouter was sent %q", query)
	}
}
