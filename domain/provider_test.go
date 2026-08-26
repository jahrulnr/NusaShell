package domain

import "testing"

func TestKindCapabilities(t *testing.T) {
	tests := []struct {
		kind       ProviderKind
		wantKey    bool
		wantModels bool
		wantEmbed  bool
		wantImage  bool
		wantSpeech bool
		wantSTT    bool
		wantVideo  bool
		wantCache  string
	}{
		{ProviderMessages, true, true, true, false, false, false, false, "anthropic"},
		{ProviderResponses, true, true, true, true, true, false, true, "openai"},
		{ProviderChat, false, true, true, true, true, true, true, "openai"},
	}
	for _, tc := range tests {
		t.Run(string(tc.kind), func(t *testing.T) {
			caps := KindCaps(tc.kind)
			if caps.RequiresKey != tc.wantKey {
				t.Errorf("RequiresKey = %v, want %v", caps.RequiresKey, tc.wantKey)
			}
			if caps.HasModelListing != tc.wantModels {
				t.Errorf("HasModelListing = %v, want %v", caps.HasModelListing, tc.wantModels)
			}
			if caps.HasEmbeddings != tc.wantEmbed {
				t.Errorf("HasEmbeddings = %v, want %v", caps.HasEmbeddings, tc.wantEmbed)
			}
			if caps.HasImageEndpoint != tc.wantImage {
				t.Errorf("HasImageEndpoint = %v, want %v", caps.HasImageEndpoint, tc.wantImage)
			}
			if caps.HasSpeechEndpoint != tc.wantSpeech {
				t.Errorf("HasSpeechEndpoint = %v, want %v", caps.HasSpeechEndpoint, tc.wantSpeech)
			}
			if caps.HasTranscriptionEndpoint != tc.wantSTT {
				t.Errorf("HasTranscriptionEndpoint = %v, want %v", caps.HasTranscriptionEndpoint, tc.wantSTT)
			}
			if caps.HasVideoEndpoint != tc.wantVideo {
				t.Errorf("HasVideoEndpoint = %v, want %v", caps.HasVideoEndpoint, tc.wantVideo)
			}
			if caps.PromptCacheStyle != tc.wantCache {
				t.Errorf("PromptCacheStyle = %q, want %q", caps.PromptCacheStyle, tc.wantCache)
			}
		})
	}
}

func TestProviderKindCapabilitiesMethod(t *testing.T) {
	p := &Provider{Kind: ProviderMessages}
	caps := p.KindCapabilities()
	if !caps.RequiresKey {
		t.Fatal("messages kind should require key")
	}
	if caps.HasImageEndpoint {
		t.Fatal("messages kind should not have image endpoint")
	}
}

func TestKindCapabilitiesUnknownKindDefaultsToChat(t *testing.T) {
	caps := KindCaps("unknown")
	if caps.RequiresKey {
		t.Error("unknown kind should default to chat (no key required)")
	}
	if !caps.HasModelListing {
		t.Error("unknown kind should default to chat (has model listing)")
	}
}

func TestValidKind(t *testing.T) {
	for _, k := range []ProviderKind{ProviderMessages, ProviderResponses, ProviderChat} {
		if !ValidKind(k) {
			t.Errorf("ValidKind(%q) = false, want true", k)
		}
	}
	if ValidKind("unknown") {
		t.Error("ValidKind(\"unknown\") = true, want false")
	}
}

func TestRequiresKey(t *testing.T) {
	if !RequiresKey(ProviderMessages) {
		t.Error("messages should require key")
	}
	if !RequiresKey(ProviderResponses) {
		t.Error("responses should require key")
	}
	if RequiresKey(ProviderChat) {
		t.Error("chat should not require key")
	}
}

func TestProviderDrivers(t *testing.T) {
	for _, driver := range []ProviderDriver{
		ProviderDriverAuto,
		ProviderDriverAnthropic,
		ProviderDriverOpenAI,
		ProviderDriverOpenRouter,
	} {
		if !ValidDriver(driver) {
			t.Errorf("ValidDriver(%q) = false", driver)
		}
	}
	if ValidDriver("unsupported") {
		t.Fatal(`ValidDriver("unsupported") = true`)
	}

	tests := []struct {
		id   string
		want ProviderDriver
	}{
		{id: "anthropic", want: ProviderDriverAnthropic},
		{id: "openai", want: ProviderDriverOpenAI},
		{id: "openrouter", want: ProviderDriverOpenRouter},
		{id: "custom", want: ProviderDriverAuto},
	}
	for _, tc := range tests {
		p := &Provider{ID: tc.id}
		if got := p.EffectiveDriver(); got != tc.want {
			t.Errorf("EffectiveDriver(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}
