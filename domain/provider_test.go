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
		wantTTLs   []string
	}{
		{ProviderMessages, true, true, true, false, false, false, false, "anthropic", []string{"5m", "1h"}},
		{ProviderResponses, true, true, true, true, true, false, true, "openai", []string{"30m"}},
		{ProviderChat, false, true, true, true, true, true, true, "openai", []string{"5m", "1h", "30m"}},
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
			if len(caps.CacheTTLs) != len(tc.wantTTLs) {
				t.Errorf("CacheTTLs = %v, want %v", caps.CacheTTLs, tc.wantTTLs)
			} else {
				for i, ttl := range tc.wantTTLs {
					if caps.CacheTTLs[i] != ttl {
						t.Errorf("CacheTTLs[%d] = %q, want %q", i, caps.CacheTTLs[i], ttl)
					}
				}
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

func TestCacheTTLsFor(t *testing.T) {
	tests := []struct {
		kind   ProviderKind
		driver ProviderDriver
		want   []string
	}{
		{ProviderMessages, ProviderDriverAnthropic, []string{"5m", "1h"}},
		{ProviderMessages, ProviderDriverOpenRouter, []string{"5m", "1h"}},
		{ProviderResponses, ProviderDriverOpenAI, []string{"30m"}},
		{ProviderResponses, ProviderDriverOpenRouter, []string{"30m"}},
		{ProviderChat, ProviderDriverOpenRouter, []string{"5m", "1h"}},
		{ProviderChat, ProviderDriverAuto, []string{"30m"}},
		{ProviderChat, ProviderDriverOpenAI, []string{"30m"}},
	}
	for _, tc := range tests {
		got := CacheTTLsFor(tc.kind, tc.driver)
		if len(got) != len(tc.want) {
			t.Errorf("CacheTTLsFor(%q,%q) = %v, want %v", tc.kind, tc.driver, got, tc.want)
			continue
		}
		for i, ttl := range tc.want {
			if got[i] != ttl {
				t.Errorf("CacheTTLsFor(%q,%q)[%d] = %q, want %q", tc.kind, tc.driver, i, got[i], ttl)
			}
		}
	}
}

func TestNormalizeCacheTTL(t *testing.T) {
	if got := NormalizeCacheTTL(ProviderMessages, ProviderDriverAnthropic, ""); got != "5m" {
		t.Errorf("empty messages TTL = %q, want 5m", got)
	}
	if got := NormalizeCacheTTL(ProviderMessages, ProviderDriverAnthropic, "1h"); got != "1h" {
		t.Errorf("messages 1h = %q, want 1h", got)
	}
	if got := NormalizeCacheTTL(ProviderResponses, ProviderDriverOpenAI, ""); got != "30m" {
		t.Errorf("empty responses TTL = %q, want 30m", got)
	}
	if got := NormalizeCacheTTL(ProviderResponses, ProviderDriverOpenAI, "1h"); got != "30m" {
		t.Errorf("invalid responses TTL must fall back to 30m, got %q", got)
	}
	if got := NormalizeCacheTTL(ProviderChat, ProviderDriverOpenRouter, "30m"); got != "5m" {
		t.Errorf("openrouter chat 30m must fall back to 5m, got %q", got)
	}
}

func TestValidCacheTTL(t *testing.T) {
	if !ValidCacheTTL(ProviderMessages, ProviderDriverAnthropic, "") {
		t.Error("empty TTL is valid (means default)")
	}
	if !ValidCacheTTL(ProviderMessages, ProviderDriverAnthropic, "1h") {
		t.Error("1h is valid for messages")
	}
	if ValidCacheTTL(ProviderMessages, ProviderDriverAnthropic, "30m") {
		t.Error("30m is not valid for messages")
	}
	if ValidCacheTTL(ProviderChat, ProviderDriverOpenRouter, "30m") {
		t.Error("30m is not valid for OpenRouter chat cache_control")
	}
	if !ValidCacheTTL(ProviderResponses, ProviderDriverOpenAI, "30m") {
		t.Error("30m is valid for responses")
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
