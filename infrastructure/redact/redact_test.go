package redact

import (
	"strings"
	"testing"
)

func TestKeyMasking(t *testing.T) {
	in := map[string]any{
		"api_key":       "sk-live-123456789012",
		"Authorization": "Bearer tok",
		"nested": map[string]any{
			"password": "hunter2",
			"safe":     "ok",
		},
		"list": []any{map[string]any{"x-api-key": "k"}},
	}
	out := Value(in).(map[string]any)
	if out["api_key"] != Mask || out["Authorization"] != Mask {
		t.Fatalf("keys not masked: %v", out)
	}
	if out["nested"].(map[string]any)["password"] != Mask {
		t.Fatal("nested key not masked")
	}
	if out["nested"].(map[string]any)["safe"] != "ok" {
		t.Fatal("safe value touched")
	}
	if out["list"].([]any)[0].(map[string]any)["x-api-key"] != Mask {
		t.Fatal("list-nested key not masked")
	}
}

func TestValuePatterns(t *testing.T) {
	cases := []struct{ in, want string }{
		{"header: Bearer abc.def.ghi", "header: " + Mask},
		{"key sk-liveabc123def456 leaked", "key " + Mask + " leaked"},
		{"google AIzaSyD4iE2xVSpkLLOXoyq2ueu", "google " + Mask},
		{"plain text, no secrets", "plain text, no secrets"},
	}
	for _, c := range cases {
		if got := String(c.in); got != c.want {
			t.Fatalf("String(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The auth-header pattern covers Bearer only — a Basic credential
// (base64 user:password, the scheme proxies and self-hosted providers
// use) survives String() verbatim inside free-text fields like
// "detail", persisting a live credential into the diagnostic log.
func TestBasicAuthValueMasked(t *testing.T) {
	got := String("upstream said: Authorization: Basic dXNlcjpwYXNz")
	if strings.Contains(got, "dXNlcjpwYXNz") {
		t.Fatalf("basic auth credential survived redaction: %q", got)
	}
}

func TestPEMBlock(t *testing.T) {
	in := "-----BEGIN PRIVATE KEY-----\nabc123\n-----END PRIVATE KEY-----"
	if got := String(in); got != Mask {
		t.Fatalf("pem not masked: %q", got)
	}
}

func TestNonSensitivePassthrough(t *testing.T) {
	in := map[string]any{"model": "x", "usage": map[string]any{"input": 100}}
	out := Value(in).(map[string]any)
	if out["model"] != "x" || out["usage"].(map[string]any)["input"] != 100 {
		t.Fatal("benign payload modified")
	}
	if strings.Contains(Mask, "real") {
		t.Fatal("unreachable")
	}
}

// Credential-bearing field names real provider and OAuth error payloads
// use — refresh_token, id_token, session_token, token, key,
// private_key — never reach the log unmasked under the default Value
// policy.
func TestCredentialFieldNames(t *testing.T) {
	in := map[string]any{
		"refresh_token": "rt-secret-123",
		"id_token":      "jwt-secret",
		"session_token": "sess-secret",
		"token":         "tok-secret",
		"key":           "generic-key-secret",
		"private_key":   "not-pem-shaped-key",
	}
	out := Value(in).(map[string]any)
	for k := range in {
		if out[k] != Mask {
			t.Fatalf("credential-shaped key %q passed through unmasked: %v", k, out[k])
		}
	}
}

// Common credential shapes beyond Bearer/sk-*/AIza*/PEM mask in
// free-form strings — provider error text embeds them verbatim.
func TestTokenShapes(t *testing.T) {
	shapes := []string{
		"ghp_abcdefghij0123456789abcdefghij",                                        // GitHub PAT
		"gho_abcdefghij0123456789abcdefghij",                                        // GitHub OAuth
		"hf_abcdefghij0123456789abcdefghij",                                         // HuggingFace
		"xoxb-123456789012-abcdefghijklmnop",                                        // Slack bot token
		"sk_live_4eC39HqLyjWDarjtT1zdp7dc",                                          // Stripe live key
		"github_pat_11AAAAAAA0abcdefghij_0123456789abcdefghijklmnopqrstuvwxyzABCDE", // GitHub fine-grained
		"ya29.a0AfB_byAbCdEfGhIjKlMnOpQrStUv",                                       // Google OAuth access token
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", // JWT
	}
	for _, s := range shapes {
		if got := String("token=" + s); got != "token="+Mask {
			t.Fatalf("unmasked token shape survived: %s → %s", s, got)
		}
	}
}

// WithoutBodies deletes the top-level "body" key and masks
// credential-shaped fields through Value — the production redactor wired
// in cmd/nusashell. Free-text fields like "detail" stay verbatim: they are
// diagnostic context, not credential-shaped names (embedded shapes still
// mask via valuePatterns).
func TestWithoutBodiesKeepsCredentialShapedFields(t *testing.T) {
	in := map[string]any{
		"status":        401,
		"body":          map[string]any{"error": "dropped"},
		"refresh_token": "rt-secret-123",
		"detail":        "bad refresh_token rt-secret-123",
		"nested":        map[string]any{"authorization": "Bearer sekrit"},
	}
	out := WithoutBodies("provider.error", in).(map[string]any)
	if _, hasBody := out["body"]; hasBody {
		t.Fatal("body should be dropped")
	}
	if out["refresh_token"] != Mask {
		t.Fatalf("refresh_token field must mask, got %v", out["refresh_token"])
	}
	if n := out["nested"].(map[string]any); n["authorization"] != Mask {
		t.Fatalf("nested authorization must mask, got %v", n["authorization"])
	}
	if out["detail"] != "bad refresh_token rt-secret-123" {
		t.Fatalf("non-credential free text must pass through, got %v", out["detail"])
	}
}

// provider.request collapses to the placeholder before any scrub — no
// byte of the body may survive, regardless of the entry's shape.
func TestWithoutBodiesProviderRequestOmitsEverything(t *testing.T) {
	for _, in := range []any{
		map[string]any{
			"model":   "m",
			"api_key": "sk-live-123",
			"messages": []any{
				map[string]any{"role": "user", "content": "Authorization: Bearer sekrit"},
			},
		},
		"a raw non-JSON string",
		[]any{"chunk1", "chunk2"},
		nil,
	} {
		out, ok := WithoutBodies("provider.request", in).(map[string]any)
		if !ok || len(out) != 1 || out["omitted"] != true {
			t.Fatalf("provider.request of %T must collapse to omitted, got %v", in, out)
		}
	}
}

// provider.response/provider.error still scrub: body drops, credential-
// shaped keys mask, and free text masks embedded token shapes.
func TestWithoutBodiesScrubsResponseAndError(t *testing.T) {
	for _, kind := range []string{"provider.response", "provider.error"} {
		in := map[string]any{
			"status":       500,
			"body":         "raw response body",
			"access_token": "tok-secret",
			"detail":       "upstream said Bearer abc.def.ghi",
		}
		out := WithoutBodies(kind, in).(map[string]any)
		if _, has := out["body"]; has {
			t.Fatalf("%s kept its body: %v", kind, out)
		}
		if out["access_token"] != Mask {
			t.Fatalf("%s left access_token unmasked: %v", kind, out)
		}
		if out["detail"] != "upstream said "+Mask {
			t.Fatalf("%s left embedded secret unmasked: %v", kind, out)
		}
		if out["status"] != 500 {
			t.Fatalf("%s clobbered benign metadata: %v", kind, out)
		}
	}
}

// Every credential shape the value patterns own must still mask — the
// prefilter can only skip a string when no pattern could match, and each
// of these carries a needle the patterns require.
func TestStringMasksEverySecretShape(t *testing.T) {
	shapes := []struct{ name, in, want string }{
		{"bearer", "Authorization: Bearer abc.def.ghi", "Authorization: " + Mask},
		{"bearer lowercase", "authorization: bearer tok123", "authorization: " + Mask},
		{"basic", "Authorization: Basic dXNlcjpwYXNz", "Authorization: " + Mask},
		{"aws akia", "key=AKIA0123456789ABCDEF", "key=" + Mask},
		{"aws asia", "key=ASIA0123456789ABCDEF", "key=" + Mask},
		{"gitlab pat", "glpat-abcdefghij0123456789", Mask},
		{"npm", "npm_0123456789abcdefghijklmnopqrstuvwxyz", Mask},
		{"openai sk", "sk-liveabc123def456", Mask},
		{"google aiza", "AIzaSyD4iE2xVSpkLLOXoyq2ueu", Mask},
		{"github ghp", "ghp_abcdefghij0123456789abcdefghij", Mask},
		{"github gho", "gho_abcdefghij0123456789abcdefghij", Mask},
		{"github ghu", "ghu_abcdefghij0123456789abcdefghij", Mask},
		{"github ghs", "ghs_abcdefghij0123456789abcdefghij", Mask},
		{"github ghr", "ghr_abcdefghij0123456789abcdefghij", Mask},
		{"github pat", "github_pat_11AAAAAAA0abcdefghij_0123456789abcdefghijklmnopqrstuvwxyzABCDE", Mask},
		{"huggingface", "hf_abcdefghij0123456789abcdefghij", Mask},
		{"slack xoxb", "xoxb-123456789012-abcdefghijklmnop", Mask},
		{"slack xoxp", "xoxp-123456789012-abcdefghijklmnop", Mask},
		{"stripe live", "sk_live_4eC39HqLyjWDarjtT1zdp7dc", Mask},
		{"stripe restricted test", "rk_test_4eC39HqLyjWDarjtT1zdp7dc", Mask},
		{"stripe webhook", "whsec_abcdefghij0123456789", Mask},
		{"google oauth", "ya29.a0AfB_byAbCdEfGhIjKlMnOpQrStUv", Mask},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.dozjgNryP4J3jVmNHl0w5N", Mask},
		{"pem", "-----BEGIN PRIVATE KEY-----\nabc123\n-----END PRIVATE KEY-----", Mask},
	}
	for _, c := range shapes {
		if got := String(c.in); got != c.want {
			t.Errorf("%s: String(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// Clean text returns untouched, and marker-looking words still reach the
// regexes — a false-negative needle would silently leak a credential.
func TestStringCleanPassthrough(t *testing.T) {
	clean := []string{
		"plain text, no secrets",
		`{"model":"x","messages":[{"role":"user","content":"hello"}]}`,
		"a basic plan upgrade", // 'basic' needle, but no base64 credential follows
		"skiing and sk-d",      // 'sk-' needle too short for the pattern
		"unicode héllo wörld — naïve éèê",
	}
	for _, in := range clean {
		if got := String(in); got != in {
			t.Errorf("String(%q) = %q, want unchanged", in, got)
		}
	}
}

func BenchmarkRedactString(b *testing.B) {
	clean := strings.Repeat("the quick brown fox jumps over the lazy dog. ", 100)
	dirty := "request failed: Authorization: Bearer abc.def.ghi " +
		"token sk-liveabc123def456 " + strings.Repeat("noise ", 200)
	markerOnly := "a basic plan upgrade, nothing sensitive " + strings.Repeat("filler ", 100)
	for _, c := range []struct {
		name string
		in   string
	}{
		{"clean", clean},
		{"dirty", dirty},
		{"marker-only", markerOnly},
	} {
		b.Run(c.name, func(b *testing.B) {
			b.SetBytes(int64(len(c.in)))
			b.ReportAllocs()
			for b.Loop() {
				_ = String(c.in)
			}
		})
	}
}

// provider.request bodies arrive fully unmarshaled (see repolog.Append);
// the redactor's cost before omitting is what this measures.
func BenchmarkWithoutBodies(b *testing.B) {
	request := map[string]any{
		"model": "provider-model",
		"messages": []any{
			map[string]any{"role": "system", "content": strings.Repeat("prompt ", 4000)},
			map[string]any{"role": "user", "content": strings.Repeat("ask ", 8000)},
			map[string]any{"role": "assistant", "content": strings.Repeat("answer ", 8000)},
			map[string]any{"role": "user", "content": strings.Repeat("followup ", 8000)},
		},
		"tools": []any{
			map[string]any{"name": "exec", "description": strings.Repeat("desc ", 1000)},
		},
	}
	b.Run("provider.request", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = WithoutBodies("provider.request", request)
		}
	})
	b.Run("provider.response", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = WithoutBodies("provider.response", request)
		}
	})
}
