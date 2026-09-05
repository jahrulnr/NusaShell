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

// Real OpenAI rejection bodies observed in the field. The "Used" figure
// appeared in mid-2025 bodies; older ones omit it entirely. The parser must
// handle both — before "Used" support, these modern bodies matched nothing,
// so the TPM overflow safety net never fired and turns spun through 5 futile
// retries before failing.
const (
	tpmBodyWithUsed = "Rate limit reached for gpt-5.6-luna in organization org-sFd4kjWg5yerL8fwlgAerbgo on tokens per min (TPM): Limit 500000, Used 271036, Requested 355391. Please try again in 15.171s. Visit https://platform.openai.com/account/rate-limits to learn more. kind=sse_transport"
	tpmBodyLegacy   = "tokens per min (TPM): Limit 200000, Requested 333331"
)

func TestParseTPMErrorWithUsed(t *testing.T) {
	limit, used, requested, ok := ParseTPMError(tpmBodyWithUsed)
	if !ok {
		t.Fatal("modern body with Used must parse")
	}
	if limit != 500000 || used != 271036 || requested != 355391 {
		t.Fatalf("ParseTPMError = (%d, %d, %d), want (500000, 271036, 355391)", limit, used, requested)
	}
}

func TestParseTPMErrorLegacyWithoutUsed(t *testing.T) {
	limit, used, requested, ok := ParseTPMError(tpmBodyLegacy)
	if !ok {
		t.Fatal("legacy body without Used must parse")
	}
	if limit != 200000 || used != 0 || requested != 333331 {
		t.Fatalf("ParseTPMError = (%d, %d, %d), want (200000, 0, 333331)", limit, used, requested)
	}
}

func TestParseTPMErrorMinuteSpellingAndNegatives(t *testing.T) {
	if _, _, _, ok := ParseTPMError("on tokens per minute: Limit 30000, Used 50, Requested 40000"); !ok {
		t.Fatal("tokens per minute spelling must parse")
	}
	if _, _, _, ok := ParseTPMError("maximum context length of 262144 tokens"); ok {
		t.Fatal("non-TPM body must not match")
	}
	if _, _, _, ok := ParseTPMError(""); ok {
		t.Fatal("empty body must not match")
	}
}

// TestParseTPMLimitRequestedWithUsed: the two-token compatibility view must
// keep working on modern bodies (it drops the Used figure).
func TestParseTPMLimitRequestedWithUsed(t *testing.T) {
	limit, requested, ok := ParseTPMLimitRequested(tpmBodyWithUsed)
	if !ok || limit != 500000 || requested != 355391 {
		t.Fatalf("ParseTPMLimitRequested = (%d, %d, %t), want (500000, 355391, true)", limit, requested, ok)
	}
}

func TestIsStructuralTPMFailureWithUsed(t *testing.T) {
	// Modern body where the request alone exceeds the budget: structural.
	if !IsStructuralTPMFailure(tpmBodyLegacy) {
		t.Fatal("legacy requested > limit must be structural")
	}
	if !IsStructuralTPMFailure("tokens per min (TPM): Limit 500000, Used 10000, Requested 600000") {
		t.Fatal("modern body with Used and requested > limit must be structural")
	}
	// The field-observed body is NOT structural: the request fits the
	// budget, retries drain the window.
	if IsStructuralTPMFailure(tpmBodyWithUsed) {
		t.Fatal("requested < limit must not be structural")
	}
}

// TestIsTPMDominatedRequest: a request consuming more than half the
// per-minute budget keeps colliding with any concurrent traffic even when it
// "fits" the raw limit — the durable fix is shrinking (compaction + learned
// cap), not waiting. Exactly half the budget is NOT dominant (it fits an
// idle window); just over half is.
func TestIsTPMDominatedRequest(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{tpmBodyWithUsed, true}, // 355391 > 500000/2
		{"tokens per min (TPM): Limit 500000, Used 0, Requested 250000", false},
		{"tokens per min (TPM): Limit 500000, Used 0, Requested 250001", true},
		{"tokens per min (TPM): Limit 200000, Requested 333331", true}, // structural ⊆ dominated
		{"rate limit exceeded", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsTPMDominatedRequest(tc.body); got != tc.want {
			t.Errorf("IsTPMDominatedRequest(%q) = %t, want %t", tc.body, got, tc.want)
		}
	}
}

func TestModelEndpointsSlugPreservesGatewayVariant(t *testing.T) {
	cases := []struct {
		name, id, canonical, want string
	}{
		{
			name:      "free variant on dated canonical",
			id:        "z-ai/glm-5.2:free",
			canonical: "z-ai/glm-5.2-20260616",
			want:      "z-ai/glm-5.2-20260616:free",
		},
		{
			name:      "paid sibling keeps canonical",
			id:        "z-ai/glm-5.2",
			canonical: "z-ai/glm-5.2-20260616",
			want:      "z-ai/glm-5.2-20260616",
		},
		{
			name:      "batch variant",
			id:        "openai/gpt-6-astra:batch",
			canonical: "openai/gpt-6-astra-20260903",
			want:      "openai/gpt-6-astra-20260903:batch",
		},
		{
			name:      "canonical already has variant",
			id:        "z-ai/glm-5.2:free",
			canonical: "z-ai/glm-5.2:free",
			want:      "z-ai/glm-5.2:free",
		},
		{
			name:      "empty canonical",
			id:        "z-ai/glm-5.2:free",
			canonical: "",
			want:      "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Model{ID: tc.id, CanonicalSlug: tc.canonical}.EndpointsSlug()
			if got != tc.want {
				t.Fatalf("EndpointsSlug() = %q, want %q", got, tc.want)
			}
		})
	}
}
