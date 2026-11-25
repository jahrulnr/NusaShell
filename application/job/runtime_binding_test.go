package job

import (
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoagent "github.com/jahrulnr/nusashell/infrastructure/repository/agent"
	repoprovider "github.com/jahrulnr/nusashell/infrastructure/repository/provider"
)

// bindDeps is a room plus one agent and one provider model, so a binding
// can move between the two runtimes the picker offers.
func bindDeps(t *testing.T) (Deps, *aggregate.Conversation) {
	t.Helper()
	prov, err := aggregate.NewProvider("deepseek", "stub", "http://unused", enum.StyleCompletion)
	if err != nil {
		t.Fatal(err)
	}
	flash, err := entity.NewModel("deepseek-v4.1-flash")
	if err != nil {
		t.Fatal(err)
	}
	prov.SetModels([]entity.Model{flash})
	providers := repoprovider.NewProviderRepository(t.TempDir(), nil)
	if err := providers.Save(t.Context(), &prov); err != nil {
		t.Fatal(err)
	}
	agent, err := aggregate.NewAgent("claude", "Claude", "claude", nil, enum.ApprovalPolicyAsk)
	if err != nil {
		t.Fatal(err)
	}
	agents := repoagent.New(t.TempDir(), nil)
	if err := agents.Save(t.Context(), &agent); err != nil {
		t.Fatal(err)
	}
	conv, err := aggregate.NewConversation("room")
	if err != nil {
		t.Fatal(err)
	}
	return Deps{Providers: providers, Agents: agents}, conv
}

// The picker's selection has to reach the room itself: a turn started from
// a chat never carries one, so a choice that only lives in the browser
// leaves a WhatsApp or Telegram message running the model the room was
// bound to last.
func TestBindRuntimeMovesBetweenRuntimes(t *testing.T) {
	d, conv := bindDeps(t)

	if err := BindRuntime(t.Context(), d, conv, RuntimeBinding{AgentID: "claude"}); err != nil {
		t.Fatal(err)
	}
	if conv.Runtime() != enum.RuntimeAgent || conv.AgentID() != "claude" {
		t.Fatalf("agent binding not applied: runtime=%v agent=%q", conv.Runtime(), conv.AgentID())
	}

	// Naming a provider route releases the agent; the agent's session and
	// options belong to it and must not survive the move.
	if err := BindRuntime(t.Context(), d, conv, RuntimeBinding{ProviderID: "deepseek", ModelID: "deepseek-v4.1-flash"}); err != nil {
		t.Fatal(err)
	}
	if conv.Runtime() != enum.RuntimeModel {
		t.Fatalf("runtime = %v, want model", conv.Runtime())
	}
	if conv.AgentID() != "" {
		t.Fatalf("agent %q survived the move to a provider", conv.AgentID())
	}
	if conv.ProviderID() != "deepseek" || conv.ModelID() != "deepseek-v4.1-flash" {
		t.Fatalf("provider binding = %q/%q", conv.ProviderID(), conv.ModelID())
	}

	// And back: an agent binding clears the provider pair it replaced.
	if err := BindRuntime(t.Context(), d, conv, RuntimeBinding{AgentID: "claude", AgentModel: "opus"}); err != nil {
		t.Fatal(err)
	}
	if conv.Runtime() != enum.RuntimeAgent || conv.AgentID() != "claude" {
		t.Fatalf("runtime = %v agent = %q", conv.Runtime(), conv.AgentID())
	}
	if conv.ProviderID() != "" {
		t.Fatalf("provider %q survived the move to an agent", conv.ProviderID())
	}
	if conv.ModelID() != "opus" {
		t.Fatalf("agent model = %q, want opus", conv.ModelID())
	}
}

// A binding the picker could show but the server cannot honour is refused
// at the picker, not left to fail on the next turn from a chat.
func TestBindRuntimeRefusesUnknownTargets(t *testing.T) {
	d, conv := bindDeps(t)
	for _, tc := range []struct {
		name    string
		binding RuntimeBinding
		want    int
	}{
		{"unknown agent", RuntimeBinding{AgentID: "nope"}, 400},
		{"unknown provider", RuntimeBinding{ProviderID: "nope", ModelID: "deepseek-v4.1-flash"}, 400},
		{"unknown model", RuntimeBinding{ProviderID: "deepseek", ModelID: "nope"}, 400},
		{"provider without model", RuntimeBinding{ProviderID: "deepseek"}, 400},
		{"empty binding", RuntimeBinding{}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := BindRuntime(t.Context(), d, conv, tc.binding)
			if got := apperror.As(err).StatusCode(); got != tc.want {
				t.Fatalf("status = %d (%v), want %d", got, err, tc.want)
			}
		})
	}
	// A refused binding must leave the room exactly as it was.
	if conv.AgentID() != "" || conv.ProviderID() != "" || conv.ModelID() != "" {
		t.Fatalf("a refused binding changed the room: agent=%q provider=%q model=%q",
			conv.AgentID(), conv.ProviderID(), conv.ModelID())
	}
}

// An agent only runs where its process can, so a room bound elsewhere
// refuses the switch instead of binding an agent that cannot start.
func TestBindRuntimeRefusesAgentOutsideLocalEnvironment(t *testing.T) {
	d, conv := bindDeps(t)
	if err := conv.SetEnvironment(enum.Environment("docker")); err != nil {
		t.Fatalf("environment %q: %v", "docker", err)
	}
	err := BindRuntime(t.Context(), d, conv, RuntimeBinding{AgentID: "claude"})
	if got := apperror.As(err).StatusCode(); got != 400 {
		t.Fatalf("status = %d (%v), want 400", got, err)
	}
	if conv.AgentID() != "" {
		t.Fatalf("agent bound in a non-local room: %q", conv.AgentID())
	}
}
