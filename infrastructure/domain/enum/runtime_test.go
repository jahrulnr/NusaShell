package enum

import "testing"

// The matrix is exhaustive for the two kinds. A value that is neither does
// not inherit the harness: a new kind is a change to these methods.
func TestRuntimeMatrix(t *testing.T) {
	type rule struct {
		name string
		fn   func(Runtime) bool
		want [2]bool // harness, agent
	}
	rules := []rule{
		{"UsesProviderRoute", Runtime.UsesProviderRoute, [2]bool{true, false}},
		{"AcceptsProviderPicks", Runtime.AcceptsProviderPicks, [2]bool{true, false}},
		{"HarnessOwnsContext", Runtime.HarnessOwnsContext, [2]bool{true, false}},
		{"MirrorsAgentCommands", Runtime.MirrorsAgentCommands, [2]bool{false, true}},
		{"QueriesAgentSessionOptions", Runtime.QueriesAgentSessionOptions, [2]bool{false, true}},
		{"ReadsAgentLaunchEnvironment", Runtime.ReadsAgentLaunchEnvironment, [2]bool{false, true}},
		{"RequiresLocalExecution", Runtime.RequiresLocalExecution, [2]bool{false, true}},
	}
	kinds := [2]Runtime{RuntimeHarness, RuntimeAgent}
	for _, rule := range rules {
		for i, kind := range kinds {
			if got := rule.fn(kind); got != rule.want[i] {
				t.Errorf("%s(%s) = %v, want %v", rule.name, kind, got, rule.want[i])
			}
		}
		if rule.fn(Runtime("other")) {
			t.Errorf("%s(other) = true, want false — an unknown kind inherits nothing", rule.name)
		}
	}
}
