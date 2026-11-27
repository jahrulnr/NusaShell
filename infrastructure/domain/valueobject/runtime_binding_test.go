package valueobject

import "testing"

func TestRuntimeBindingRefusesEmptyPartialAndMixed(t *testing.T) {
	if _, err := NewAgentBinding("", "opus"); err == nil {
		t.Fatal("an agent binding with no agent id was built")
	}
	if _, err := NewAgentBinding("  ", ""); err == nil {
		t.Fatal("a blank agent id was built")
	}
	if _, err := NewHarnessBinding("", "m1"); err == nil {
		t.Fatal("a provider binding with no provider was built")
	}
	if _, err := NewHarnessBinding("gw1", ""); err == nil {
		t.Fatal("a provider binding with no model was built")
	}
	if _, err := NewHarnessBinding("", ""); err == nil {
		t.Fatal("an empty provider binding was built")
	}
	agent, err := NewAgentBinding("claude", "")
	if err != nil {
		t.Fatal(err)
	}
	if agent.Kind().String() != "agent" || agent.AgentID() != "claude" || agent.ProviderID() != "" || agent.ModelID() != "" {
		t.Fatalf("agent binding = %+v", agent)
	}
	if agent.AgentModel() != "" {
		t.Fatal("the agent's model is optional")
	}
	withModel, err := NewAgentBinding("claude", "opus")
	if err != nil || withModel.AgentModel() != "opus" || withModel.ModelID() != "" {
		t.Fatalf("agent model = %+v, %v", withModel, err)
	}
	harness, err := NewHarnessBinding("gw1", "m1")
	if err != nil {
		t.Fatal(err)
	}
	if !harness.Kind().UsesProviderRoute() || harness.ProviderID() != "gw1" || harness.ModelID() != "m1" || harness.AgentID() != "" {
		t.Fatalf("harness binding = %+v", harness)
	}
	var zero RuntimeBinding
	if !zero.IsZero() || zero.Kind().UsesProviderRoute() || zero.Kind().MirrorsAgentCommands() {
		t.Fatal("the zero binding must be absence, not a kind")
	}
}
