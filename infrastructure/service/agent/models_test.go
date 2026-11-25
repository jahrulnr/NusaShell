package agent

import (
	"encoding/json"
	"testing"
)

// A set_config_option response names what the write changed. Folding it into the
// session's catalog must keep the options it does not mention — treating it as
// the offered list narrows the picker to whatever the agent echoed.
func TestMergeConfigOptionsKeepsWhatTheResponseOmits(t *testing.T) {
	stored := json.RawMessage(`[
		{"id":"model","type":"select","currentValue":"fake-fast","options":[{"value":"fake-fast"},{"value":"fake-smart"}]},
		{"id":"mode","type":"select","currentValue":"ask","options":[{"value":"ask"},{"value":"accept-edits"}]},
		{"id":"effort","type":"select","currentValue":"high","options":[{"value":"low"},{"value":"high"}]}
	]`)
	// The agent answered with the entry it changed and nothing else.
	update := json.RawMessage(`[{"id":"mode","type":"select","currentValue":"accept-edits","options":[{"value":"ask"},{"value":"accept-edits"}]}]`)

	merged := absorbConfigResponse(stored, update)
	for _, id := range []string{"model", "mode", "effort"} {
		if !json.Valid(merged) || !containsOption(t, merged, id) {
			t.Fatalf("option %q vanished from the catalog: %s", id, merged)
		}
	}
	if got := currentOf(t, merged, "mode"); got != "accept-edits" {
		t.Fatalf("mode = %q, want the value the write set", got)
	}
	if got := currentOf(t, merged, "effort"); got != "high" {
		t.Fatalf("effort = %q, want it untouched", got)
	}
}

// An entry that carries only an id says nothing: it must not erase the option
// the session already knows (the "Select Value" entry an agent's blank echo left
// behind).
func TestMergeConfigOptionsIgnoresABareId(t *testing.T) {
	stored := json.RawMessage(`[{"id":"speed","type":"select","currentValue":"normal","options":[{"value":"normal"},{"value":"fast"}]}]`)
	update := json.RawMessage(`[{"id":"speed","currentValue":null}]`)
	merged := absorbConfigResponse(stored, update)
	if got := currentOf(t, merged, "speed"); got != "normal" {
		t.Fatalf("speed = %q after a bare echo, want its stored value", got)
	}
	if !containsOption(t, merged, "speed") || !hasChoices(t, merged, "speed") {
		t.Fatalf("a bare echo erased the option's choices: %s", merged)
	}
}

// A model switch answers with the model entry alone when the agent works that
// way; the options it does not mention stay offered.
func TestModelSwitchKeepsTheCatalogAPartialResponseOmits(t *testing.T) {
	t.Setenv("FAKE_ACP_PARTIAL_CONFIG", "1")
	rt, in := setup(t, "hello")
	ctx := t.Context()
	liveMode(t, ctx, rt, in) // open the session
	before, _, err := rt.Options(ctx, in.Agent, in.Conversation.ID())
	if err != nil {
		t.Fatal(err)
	}
	in.Model = "fake-smart"
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	after, _, err := rt.Options(ctx, in.Agent, in.Conversation.ID())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, o := range after {
		seen[o.ID] = true
	}
	for _, o := range before {
		if !seen[o.ID] {
			t.Fatalf("option %q vanished after a model switch: before=%+v after=%+v", o.ID, before, after)
		}
	}
}

func containsOption(t *testing.T, raw json.RawMessage, id string) bool {
	t.Helper()
	for _, o := range decodeOptions(t, raw) {
		if o["id"] == id {
			return true
		}
	}
	return false
}

func currentOf(t *testing.T, raw json.RawMessage, id string) string {
	t.Helper()
	for _, o := range decodeOptions(t, raw) {
		if o["id"] == id {
			value, _ := o["currentValue"].(string)
			return value
		}
	}
	return ""
}

func hasChoices(t *testing.T, raw json.RawMessage, id string) bool {
	t.Helper()
	for _, o := range decodeOptions(t, raw) {
		if o["id"] == id {
			choices, _ := o["options"].([]any)
			return len(choices) > 0
		}
	}
	return false
}

func decodeOptions(t *testing.T, raw json.RawMessage) []map[string]any {
	t.Helper()
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("catalog is not JSON: %v (%s)", err, raw)
	}
	return list
}

// An agent that answers a write with only the changed entry must not shrink the
// picker: every option the session offered stays on the list.
func TestSetOptionKeepsTheCatalogAPartialResponseOmits(t *testing.T) {
	t.Setenv("FAKE_ACP_PARTIAL_CONFIG", "1")
	rt, in := setup(t, "hello")
	ctx := t.Context()
	mode := liveMode(t, ctx, rt, in)
	before, _, err := rt.Options(ctx, in.Agent, in.Conversation.ID())
	if err != nil {
		t.Fatal(err)
	}
	next := ""
	for _, c := range mode.Choices {
		if c.Value != mode.Current {
			next = c.Value
			break
		}
	}
	if err := rt.SetOption(ctx, in.Conversation.ID(), mode.ID, next); err != nil {
		t.Fatal(err)
	}
	after, _, err := rt.Options(ctx, in.Agent, in.Conversation.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("a partial response shrank the catalog: %d options before, %d after\nbefore=%+v\nafter=%+v", len(before), len(after), before, after)
	}
	// Every option the session offered before the write is still offered.
	seen := map[string]bool{}
	for _, o := range after {
		seen[o.ID] = true
	}
	for _, o := range before {
		if !seen[o.ID] {
			t.Fatalf("option %q vanished after a partial response: before=%+v after=%+v", o.ID, before, after)
		}
	}
}
