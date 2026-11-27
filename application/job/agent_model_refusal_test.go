package job

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoagent "github.com/jahrulnr/nusashell/infrastructure/repository/agent"
)

// refuseThenRun refuses one model id and runs the next prompt, recording
// the model each prompt was given.
type refuseThenRun struct {
	dservice.AgentRuntime
	models []string
}

func (r *refuseThenRun) Prompt(_ context.Context, p dservice.AgentPrompt, _ event.TurnEventHandler, _ dservice.Approver) error {
	r.models = append(r.models, p.Model)
	if p.Model == "grok-4.7-high" {
		return apperror.New(400, "the agent does not offer this model", fmt.Errorf("model %q", p.Model))
	}
	m, err := entity.NewMessage(enum.RoleAssistant, "ran")
	if err != nil {
		return err
	}
	m.SetFinishReason(enum.FinishReasonStop)
	p.Conversation.Append(m)
	return nil
}

func (r *refuseThenRun) Steer(context.Context, string, valueobject.UserInput, string, func()) bool {
	return false
}

// A refused model is an assistant line in the thread, and the room does not
// keep that id. The next message starts a turn on the agent's own model.
func TestRefusedAgentModelIsVisibleAndNotReused(t *testing.T) {
	rt := &refuseThenRun{}
	d := hydrationDeps(t, nil)
	agent, err := aggregate.NewAgent("claude", "Claude", "claude", nil, enum.ApprovalPolicyAsk)
	if err != nil {
		t.Fatal(err)
	}
	agents := repoagent.New(t.TempDir(), nil)
	if err := agents.Save(t.Context(), &agent); err != nil {
		t.Fatal(err)
	}
	d.Agents = agents
	d.AgentRuntime = rt

	first := &Send{
		Deps: d, Ctx: t.Context(), Receipt: NewPersistenceReceipt(),
		ModelID: "grok-4.7-high", Message: "hello",
		Opts: SendOptions{Agent: "claude"},
	}
	first.Bind("room", nil)
	err = first.Execute(t.Context())
	if apperror.As(err).StatusCode() != 400 {
		t.Fatalf("first turn = %v, want the model refusal", err)
	}
	conv, err := d.Convs.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	if conv.ModelID() == "grok-4.7-high" {
		t.Fatal("the rejected model was stored")
	}
	seen := false
	for _, m := range conv.Messages() {
		if m.Role() == enum.RoleAssistant && m.Content() != "" {
			if !strings.Contains(m.Content(), "does not offer this model") || !strings.Contains(m.Content(), "grok-4.7-high") {
				t.Fatalf("visible error = %q", m.Content())
			}
			seen = true
		}
	}
	if !seen {
		t.Fatal("the refusal is not an assistant message in the thread")
	}
	info, err := d.Convs.Info(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	if info.Terminal != "failed" {
		t.Fatalf("terminal = %q, want failed", info.Terminal)
	}

	second := &Send{
		Deps: d, Ctx: t.Context(), Receipt: NewPersistenceReceipt(),
		Message: "again", Opts: SendOptions{Agent: "claude"},
	}
	second.Bind("room", nil)
	if err := second.Execute(t.Context()); err != nil {
		t.Fatalf("later message did not start a turn: %v", err)
	}
	if len(rt.models) != 2 || rt.models[0] != "grok-4.7-high" || rt.models[1] == "grok-4.7-high" {
		t.Fatalf("prompts ran as %v, want the refusal then a turn that does not reuse it", rt.models)
	}
}
