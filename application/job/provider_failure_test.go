package job

import (
	"context"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoconversation "github.com/jahrulnr/nusashell/infrastructure/repository/conversation"
)

// refuseSvc is an upstream that rejects the turn and writes no assistant line.
type refuseSvc struct{}

func (refuseSvc) Send(context.Context, *aggregate.Conversation, *aggregate.Provider, entity.Model, event.TurnEventHandler) error {
	return apperror.New(500, "qa upstream refused the turn", nil)
}

func TestProviderRefusalIsAnAssistantMessage(t *testing.T) {
	d := Deps{
		SummaryTool: mustSummaryTool(t),
		Convs:       repoconversation.NewConversationRepository(t.TempDir()),
		Config:      config.ConversationConfig{MaxTurnStep: 2},
	}
	conv, err := aggregate.NewConversation("room")
	if err != nil {
		t.Fatal(err)
	}
	user, err := entity.NewMessage(enum.RoleUser, "PLEASE-FAIL the first turn")
	if err != nil {
		t.Fatal(err)
	}
	conv.Append(user)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Convs.AppendMessage(t.Context(), conv.ID(), user); err != nil {
		t.Fatal(err)
	}
	err = d.runTurn(t.Context(), conv.ID(), conv, EnvironmentBundle{}, nil, entity.Model{}, refuseSvc{}, nil, nil, valueobject.TurnOrigin{})
	if apperror.As(err).StatusCode() != 500 {
		t.Fatalf("refusal = %v, want 500", err)
	}
	loaded, err := d.Convs.Load(t.Context(), conv.ID())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Terminal() != "failed" {
		t.Fatalf("terminal = %q, want failed", loaded.Terminal())
	}
	for _, m := range loaded.Messages() {
		if m.Role() == enum.RoleAssistant && m.Content() == "qa upstream refused the turn" {
			return
		}
	}
	t.Fatal("the refusal is not an assistant message in the thread")
}
