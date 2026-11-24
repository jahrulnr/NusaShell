// Package redact scrubs credential material out of diagnostic payloads —
// raw provider bodies echo request headers, error details quote auth
// headers, so anything a Logger persists passes through here first.
// Pure functions over JSON-shaped values; lives in infrastructure because
// the payloads it scrubs are wire shapes the domain never sees.
package redact

import (
	"encoding/json"
	"regexp"
)

// Mask replaces sensitive material.
const Mask = "***"

// keyPattern matches map keys that carry credentials — case-insensitive,
// tolerant of separators (api_key, api-key, apikey). The token/key/private
// alternatives are deliberately broad: OAuth and provider error payloads
// name credential fields refresh_token, id_token, session_token, token,
// key, and private_key — over-masking a "key" field in diagnostics beats
// logging a live credential.
var keyPattern = regexp.MustCompile(`(?i)(api[-_]?key|access[-_]?token|auth[-_]?token|authorization|bearer|credential|password|secret|token|key|private|cookie)`)

// valuePatterns mask secrets hiding in otherwise-innocent string fields —
// token shapes, auth header values, PEM blocks.
var valuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+\S+`),                                           // Authorization: Bearer
	regexp.MustCompile(`(?i)\bbasic\s+[A-Za-z0-9+/=]{8,}`),                           // Authorization: Basic <user:pass>
	regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),                              // AWS access key ids
	regexp.MustCompile(`\bglpat-[A-Za-z0-9\-_]{20,}`),                                // GitLab personal access tokens
	regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`),                                    // npm access tokens
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}`),                                     // OpenAI/OpenRouter-style keys
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{20,}`),                                   // Google API keys
	regexp.MustCompile(`\bgh[opsur]_[A-Za-z0-9]{20,}`),                               // GitHub ghp_/gho_/ghu_/ghs_/ghr_
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`),                             // GitHub fine-grained PAT
	regexp.MustCompile(`\bhf_[A-Za-z0-9]{20,}`),                                      // HuggingFace
	regexp.MustCompile(`\bxox[bapors]-[A-Za-z0-9-]{10,}`),                            // Slack tokens
	regexp.MustCompile(`\b[sr]k_(live|test)_[A-Za-z0-9]{10,}`),                       // Stripe secret/restricted keys
	regexp.MustCompile(`\bwhsec_[A-Za-z0-9]{10,}`),                                   // Stripe-style webhook secrets
	regexp.MustCompile(`\bya29\.[A-Za-z0-9_-]{10,}`),                                 // Google OAuth access tokens
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`), // JWTs
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
}

// Value deep-scrubs a JSON-shaped value (map[string]any, []any, string,
// scalars). Sensitive keys and secret-looking strings are masked; other
// values pass through untouched. Unknown nested types are left as-is —
// callers marshal to JSON anyway.
func Value(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			if keyPattern.MatchString(k) {
				out[k] = Mask
				continue
			}
			out[k] = Value(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = Value(x)
		}
		return out
	case string:
		return String(t)
	default:
		return v
	}
}

// secretNeedles is the case-insensitive fast-reject list for String: each
// valuePatterns alternative requires one of these ASCII needles verbatim,
// so a string holding none of them cannot mask and can skip the regex
// battery. Keep them lowercase — the scan folds subject bytes to compare.
var secretNeedles = []string{
	"akia", "asia", // AWS access key id prefixes
	"aiza",            // Google API keys
	"basic", "bearer", // Authorization schemes
	"eyj",                   // JWT header
	"github_pat_", "glpat-", // GitHub/GitLab PATs
	"ghp_", "gho_", "ghu_", "ghs_", "ghr_", // GitHub tokens
	"hf_", "npm_", "sk-", "whsec_", "xox", "ya29.", // provider tokens
	"_live_", "_test_", // Stripe sk_live_/rk_test_
	"private key", // PEM blocks
}

// needlesByFirstByte buckets secretNeedles by leading byte so the scan
// compares only the handful of candidates that could start at a position.
var needlesByFirstByte = func() (m [256][]string) {
	for _, n := range secretNeedles {
		m[n[0]] = append(m[n[0]], n)
	}
	return
}()

// mightContainSecret reports whether s holds a needle some secret pattern
// needs. One alloc-free pass over ASCII-folded bytes — the common case
// (payloads with no credential material) avoids seventeen regex scans.
func mightContainSecret(s string) bool {
	for i := 0; i < len(s); i++ {
		for _, n := range needlesByFirstByte[lowerASCII(s[i])] {
			if len(n) <= len(s)-i && foldHasPrefix(s[i:], n) {
				return true
			}
		}
	}
	return false
}

// foldHasPrefix reports whether s begins with the lowercase ASCII needle,
// folding only ASCII letters — multibyte runes can never match a needle
// byte, so they fall through harmlessly.
func foldHasPrefix(s, n string) bool {
	for i := 0; i < len(n); i++ {
		if lowerASCII(s[i]) != n[i] {
			return false
		}
	}
	return true
}

func lowerASCII(b byte) byte {
	if 'A' <= b && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// String masks secret patterns inside a free-form string — the value of a
// non-sensitive key may still contain a token.
func String(s string) string {
	if !mightContainSecret(s) {
		return s
	}
	for _, p := range valuePatterns {
		s = p.ReplaceAllString(s, Mask)
	}
	return s
}

// WithoutBodies is a Logger Options.Redact policy: it keeps diagnostic
// metadata without persisting conversation content. provider.request is
// logged as unmarshaled JSON (the whole body), not a map with a "body"
// key — omit it entirely, and skip the deep scrub since every byte of it
// is discarded anyway. Response/error maps still drop only their "body"
// field so status and request_id survive.
func WithoutBodies(kind string, e any) any {
	if kind == "provider.request" {
		return map[string]any{"omitted": true}
	}
	e = Value(e)
	switch v := e.(type) {
	case map[string]any:
		delete(v, "body")
		return v
	case []json.RawMessage:
		return map[string]any{"chunks": len(v)}
	case []any:
		return map[string]any{"chunks": len(v)}
	default:
		return map[string]any{"omitted": true}
	}
}
