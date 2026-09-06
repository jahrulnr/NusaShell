package application

import (
	"testing"

	"nusashell/domain"
)

// TestToCoreRequestProviderRouteInjection verifies that a pinned upstream
// route is translated to the OpenRouter provider object only when the
// gateway is an aggregator AND the route is non-empty — direct providers
// and auto routing must never carry the provider field.
func TestToCoreRequestProviderRouteInjection(t *testing.T) {
	base := ChatRequest{
		Model:         "meta-llama/llama-3.3-70b-instruct",
		ProviderRoute: "nebius/fp8",
		Messages:      []ChatMessage{{Role: "user", Content: "hi"}},
	}

	// 1) OpenRouter chat + route → provider object with strict pinning.
	cr := ToCoreRequest(base, domain.ProviderChat, true)
	raw, ok := cr.ProviderOptions["provider"]
	if !ok {
		t.Fatal("provider option missing for openrouter+route")
	}
	routing, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("provider option = %T, want map", raw)
	}
	order, ok := routing["order"].([]string)
	if !ok || len(order) != 1 || order[0] != "nebius/fp8" {
		t.Fatalf("order = %#v, want [nebius/fp8]", routing["order"])
	}
	if ff, ok := routing["allow_fallbacks"].(bool); !ok || ff {
		t.Fatalf("allow_fallbacks = %#v, want false", routing["allow_fallbacks"])
	}

	// 2) OpenRouter + empty route → no provider field (auto/load balance).
	auto := base
	auto.ProviderRoute = ""
	cr = ToCoreRequest(auto, domain.ProviderChat, true)
	if _, ok := cr.ProviderOptions["provider"]; ok {
		t.Fatal("provider option must be omitted for auto routing")
	}

	// 3) OpenRouter + whitespace route → treated as auto.
	ws := base
	ws.ProviderRoute = "   "
	cr = ToCoreRequest(ws, domain.ProviderChat, true)
	if _, ok := cr.ProviderOptions["provider"]; ok {
		t.Fatal("provider option must be omitted for whitespace route")
	}

	// 4) Non-aggregator provider (Anthropic messages) + route → never sent.
	cr = ToCoreRequest(base, domain.ProviderMessages, false)
	if _, ok := cr.ProviderOptions["provider"]; ok {
		t.Fatal("provider option must not reach direct providers")
	}

	// 5) Aggregator but non-chat wire (messages via OpenRouter) → the
	// provider object is a chat-completions extension; keep it clean.
	cr = ToCoreRequest(base, domain.ProviderMessages, true)
	if _, ok := cr.ProviderOptions["provider"]; ok {
		t.Fatal("provider option must not be injected for non-chat kinds")
	}
}
