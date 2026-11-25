package appconversation

import (
	"context"
	"strings"
	"unicode"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/prompt"
)

// titleInputRunes bounds each side of the exchange the utility model
// reads; the start of a message says what it is about.
const titleInputRunes = 2000

// titleMaxRunes bounds a generated title.
const titleMaxRunes = 80

// UseUtility sets where the utility model comes from. fn is read on each
// use, so a change in Settings applies at once; nil or an empty answer
// turns utility tasks off.
func (app *ConversationApp) UseUtility(fn func() (providerID, modelID string)) {
	app.mu.Lock()
	app.utility = fn
	app.mu.Unlock()
}

// Utility asks the utility model for one plain-text answer: no tools, no
// history, nothing kept. Without a utility model it is a 409.
func (app *ConversationApp) Utility(ctx context.Context, convID, system, user string) (string, error) {
	app.mu.Lock()
	utility := app.utility
	app.mu.Unlock()
	providerID, modelID := "", ""
	if utility != nil {
		providerID, modelID = utility()
	}
	if providerID == "" || modelID == "" {
		return "", apperror.New(409, "no utility model is assigned — Settings → Models", nil)
	}
	return app.depsSnapshot().Generate(ctx, convID, providerID, modelID, system, user)
}

// AutoTitle names a thread after its first exchange, using the utility
// model. It does nothing (and answers nil) when no utility model is set,
// when the thread is past its first turn, or when its title is no longer
// the one taken from the first message — the owner renamed it, and that
// stays. A title is never worth failing anything over: callers log the
// error and keep the default title.
func (app *ConversationApp) AutoTitle(ctx context.Context, convID string) error {
	app.mu.Lock()
	utility := app.utility
	app.mu.Unlock()
	if utility == nil {
		return nil
	}
	providerID, modelID := utility()
	if providerID == "" || modelID == "" {
		return nil
	}
	deps := app.depsSnapshot()
	conv, err := deps.Convs.Load(ctx, convID)
	if err != nil {
		return err
	}
	var asked, answer string
	users := 0
	for _, m := range conv.Messages() {
		switch {
		case m.Role() == enum.RoleUser && m.UserType() != enum.LLMUserTypeSystem:
			users++
			if users == 1 {
				asked = m.Content()
			}
		case m.Role() == enum.RoleAssistant && strings.TrimSpace(m.Content()) != "":
			answer = m.Content()
		}
	}
	// The default title is the first message's head, normalized by
	// SetTitle (see the send job and the aggregate's title rule).
	if users != 1 || !conv.TitleIsDefault() {
		return nil
	}
	exchange := "User:\n" + clip(asked, titleInputRunes) + "\n\nAssistant:\n" + clip(answer, titleInputRunes)
	raw, err := deps.Generate(ctx, convID, providerID, modelID, prompt.SystemPrompt("title"), exchange)
	if err != nil {
		return err
	}
	title := cleanTitle(raw)
	if title == "" {
		return nil
	}
	from := conv.Title()
	// Annotate announces the changed room itself — the notice belongs to the
	// mutation, not to each caller of it.
	return app.Annotate(ctx, convID, Annotation{Title: &title, IfTitle: &from})
}

// cleanTitle keeps the first line of a model's answer, without the
// quotes, Markdown, or closing period models add despite being asked not
// to, and bounds its length.
func cleanTitle(raw string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(raw), "\n")
	line = strings.TrimLeft(line, "#*-> ")
	line = strings.TrimPrefix(line, "Title:")
	line = strings.TrimFunc(line, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("\"'`*_“”‘’.", r)
	})
	return clip(line, titleMaxRunes)
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
