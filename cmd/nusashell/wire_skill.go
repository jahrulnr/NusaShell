package main

import (
	"log/slog"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/application/skill"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	"github.com/jahrulnr/nusashell/infrastructure/redact"
)

// wireSkillObserve is phase 0 of skill improvement. Off returns nil and touches
// nothing: no store, no observer, no hook. Observe registers one finished hook,
// which the hub runs on its own goroutine, and returns the edit evidence the
// room broker records into.
func wireSkillObserve(mode string, onFinished func(func(convID, kind string, origin valueobject.TurnOrigin, external bool, err error)), convs drepo.Conversation, log *slog.Logger) appjob.EditEvidence {
	if mode != "observe" {
		return nil
	}
	ev := skill.NewEvidence()
	obs := skill.NewObserver(convs, ev, redact.String, log)
	onFinished(func(convID, kind string, origin valueobject.TurnOrigin, _ bool, err error) {
		obs.Observe(convID, kind, origin, err)
	})
	return ev
}
