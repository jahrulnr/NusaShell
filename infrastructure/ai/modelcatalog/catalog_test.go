package modelcatalog

import "testing"

func TestDetectKind(t *testing.T) {
	cases := []struct {
		name string
		id   string
		cm   catalogModel
		want string
	}{
		{
			name: "chat text only",
			id:   "deepseek/deepseek-v4-flash",
			cm:   catalogModel{Modalities: modalityIO([]string{"text"}, []string{"text"})},
			want: "chat",
		},
		{
			name: "chat with vision (image input → text output)",
			id:   "anthropic/claude-opus-4.7",
			cm:   catalogModel{Modalities: modalityIO([]string{"text", "image"}, []string{"text"})},
			want: "chat",
		},
		{
			name: "embedding by name pattern",
			id:   "baai/bge-m3",
			cm:   catalogModel{Modalities: modalityIO([]string{"text"}, []string{"text"})},
			want: "embedding",
		},
		{
			name: "embedding by name 'embed'",
			id:   "nomic-embed-text",
			cm:   catalogModel{Modalities: modalityIO([]string{"text"}, []string{"text"})},
			want: "embedding",
		},
		{
			name: "embedding by description",
			id:   "nvidia/nv-embed-v1",
			cm: catalogModel{
				Description: "Embedding model for semantic search, retrieval, clustering, and ranking pipelines",
				Modalities:  modalityIO([]string{"text"}, []string{"text"}),
			},
			want: "embedding",
		},
		{
			name: "image generation (text → image only)",
			id:   "black-forest-labs/flux.1-dev",
			cm:   catalogModel{Modalities: modalityIO([]string{"text"}, []string{"image"})},
			want: "image",
		},
		{
			name: "gpt-image by name even without modalities",
			id:   "openai/gpt-image-1",
			cm:   catalogModel{},
			want: "image",
		},
		{
			name: "image gen with image input (edit)",
			id:   "qwen/qwen-image-edit",
			cm:   catalogModel{Modalities: modalityIO([]string{"text", "image"}, []string{"image"})},
			want: "image",
		},
		{
			name: "image+text output is NOT image gen (multimodal LLM)",
			id:   "gemini-3.0-pro-image-preview",
			cm:   catalogModel{Modalities: modalityIO([]string{"text", "image"}, []string{"image", "text"})},
			want: "chat",
		},
		{
			name: "video generation",
			id:   "google/veo-3.1",
			cm:   catalogModel{Modalities: modalityIO([]string{"text"}, []string{"video"})},
			want: "video",
		},
		{
			name: "tts (text → audio only)",
			id:   "mimo-v2-tts",
			cm:   catalogModel{Modalities: modalityIO([]string{"text"}, []string{"audio"})},
			want: "tts",
		},
		{
			name: "stt (audio → text, no image input)",
			id:   "openai/whisper-large-v3",
			cm:   catalogModel{Modalities: modalityIO([]string{"audio"}, []string{"text"})},
			want: "stt",
		},
		{
			name: "multimodal with audio+image input is chat, not stt",
			id:   "gemini-2.0-flash",
			cm:   catalogModel{Modalities: modalityIO([]string{"audio", "image", "text"}, []string{"text"})},
			want: "chat",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectKind(tc.id, tc.cm)
			if got != tc.want {
				t.Errorf("detectKind(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

func modalityIO(in, out []string) struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
} {
	return struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	}{Input: in, Output: out}
}

func TestLookupStripsProviderPrefixFromQuery(t *testing.T) {
	c := &Catalog{
		loaded:   true,
		byID:     map[string]*ModelMetadata{"google/gemini-3.7-flash": {ID: "google/gemini-3.7-flash", Kind: "chat"}},
		byBareID: map[string]*ModelMetadata{"gemini-3.7-flash": {ID: "google/gemini-3.7-flash", Kind: "chat"}},
		byName:   map[string]*ModelMetadata{},
	}
	// Query from a different gateway with its own prefix
	got := c.Lookup("", "tokenrouter/gemini-3.7-flash")
	if got == nil {
		t.Fatal("Lookup returned nil for tokenrouter/gemini-3.7-flash")
	}
	if got.ID != "google/gemini-3.7-flash" {
		t.Errorf("Lookup matched wrong model: got %q, want google/gemini-3.7-flash", got.ID)
	}
}

func TestLookupByDisplayName(t *testing.T) {
	c := &Catalog{
		loaded:   true,
		byID:     map[string]*ModelMetadata{},
		byBareID: map[string]*ModelMetadata{},
		byName:   map[string]*ModelMetadata{"gemini 3.7 flash": {ID: "google/gemini-3.7-flash", Kind: "chat"}},
	}
	got := c.Lookup("", "Gemini 3.7 Flash")
	if got == nil {
		t.Fatal("Lookup by display name returned nil")
	}
	if got.ID != "google/gemini-3.7-flash" {
		t.Errorf("Lookup matched wrong model: got %q", got.ID)
	}
}

func TestLookupStripsProviderSuffix(t *testing.T) {
	c := &Catalog{
		loaded:   true,
		byID:     map[string]*ModelMetadata{"qwen/qwen3.8-max": {ID: "qwen/qwen3.8-max", Kind: "chat", SupportedEfforts: []string{"low", "medium", "high"}}},
		byBareID: map[string]*ModelMetadata{"qwen3.8-max": {ID: "qwen/qwen3.8-max", Kind: "chat", SupportedEfforts: []string{"low", "medium", "high"}}},
		byName:   map[string]*ModelMetadata{},
	}
	tests := []struct {
		query string
		want  string
	}{
		{"qwen/qwen3.8-max:free", "qwen/qwen3.8-max"},
		{"qwen/qwen3.8-max-free", "qwen/qwen3.8-max"},
		{"qwen3.8-max:free", "qwen/qwen3.8-max"},
		{"qwen3.8-max-free", "qwen/qwen3.8-max"},
		{"qwen/qwen3.8-max:nitro", "qwen/qwen3.8-max"},
		{"qwen/qwen3.8-max", "qwen/qwen3.8-max"}, // no suffix — exact match
	}
	for _, tt := range tests {
		got := c.Lookup("", tt.query)
		if got == nil {
			t.Errorf("Lookup(%q) returned nil, want %q", tt.query, tt.want)
			continue
		}
		if got.ID != tt.want {
			t.Errorf("Lookup(%q) = %q, want %q", tt.query, got.ID, tt.want)
		}
	}
}
