package job

import (
	"context"
	"fmt"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Generate asks a provider's model for one plain-text answer to a single
// instruction: no tools, no history, nothing persisted. Utility tasks
// such as naming a thread use it; convID only labels the call.
func (d Deps) Generate(ctx context.Context, convID, providerID, modelID, system, user string) (string, error) {
	scratch, err := aggregate.NewConversation(convID)
	if err != nil {
		return "", err
	}
	driver, err := d.environment(scratch)
	if err != nil {
		return "", err
	}
	prov, model, svc, err := d.resolveProvider(ctx, convID, scratch, driver, providerID, modelID)
	if err != nil {
		return "", err
	}
	scratch.SetSystemPrompt(system)
	msg, err := entity.NewMessage(enum.RoleUser, user)
	if err != nil {
		return "", err
	}
	scratch.Append(msg)
	if err := svc.Send(ctx, scratch, prov, model.ApplyUnknownContextDefaults(), nil); err != nil {
		return "", err
	}
	msgs := scratch.Messages()
	if len(msgs) < 2 {
		return "", apperror.New(502, "the model gave no answer", fmt.Errorf("provider %s model %s appended nothing", providerID, modelID))
	}
	return strings.TrimSpace(msgs[len(msgs)-1].Content()), nil
}
