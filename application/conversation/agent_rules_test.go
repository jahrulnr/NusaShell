package appconversation

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoapprovalrule "github.com/jahrulnr/nusashell/infrastructure/repository/approvalrule"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

// sendAnswering runs one agent turn and answers its approval with
// optionID; it reports whether the owner was asked at all.
func sendAnswering(t *testing.T, app *ConversationApp, convID, text, optionID string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	asked := false
	onEvent := func(ev event.TurnEvent) {
		if ev.Kind() == enum.TurnEventApproval {
			asked = true
			go func() { _ = app.Approve(convID, ev.Approval().ID(), optionID) }()
		}
	}
	if _, err := app.Send(ctx, convID, "", "", text, nil, appjob.SendOptions{Agent: "fake"}, onEvent); err != nil {
		t.Fatal(err)
	}
	return asked
}

// "Always allow" stores a rule for the room and kind; later turns are
// answered by it until the owner deletes it.
func TestApprovalRules(t *testing.T) {
	app, _, _ := agentFixture(t, enum.ApprovalPolicyAsk)
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "app.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	app.UseApprovalRules(repoapprovalrule.New(db))
	ctx := t.Context()

	if !sendAnswering(t, app, "c_rules", "look", "yes") {
		t.Fatal("the first turn must ask")
	}
	if rules, _ := app.ApprovalRules(ctx, "c_rules"); len(rules) != 0 {
		t.Fatalf("allow once stored a rule: %+v", rules)
	}
	if !sendAnswering(t, app, "c_rules", "look again", "always") {
		t.Fatal("allow once must not answer later turns")
	}
	rules, err := app.ApprovalRules(ctx, "c_rules")
	if err != nil || len(rules) != 1 || rules[0].Action() != enum.ToolActionRead || rules[0].AgentID() != "fake" {
		t.Fatalf("rules = %+v, %v", rules, err)
	}
	if sendAnswering(t, app, "c_rules", "and again", "no") {
		t.Fatal("a stored rule must answer without asking")
	}
	msgs, _ := app.History(ctx, "c_rules", 0, 20)
	if last := msgs[len(msgs)-1]; last.Content() != "found README.md" {
		t.Fatalf("the rule did not allow the call: %q", last.Content())
	}
	if !sendAnswering(t, app, "c_other_room", "look", "yes") {
		t.Fatal("a rule holds only in its own room")
	}
	if err := app.DeleteApprovalRule(ctx, "c_rules", rules[0].ID()); err != nil {
		t.Fatal(err)
	}
	if !sendAnswering(t, app, "c_rules", "once more", "yes") {
		t.Fatal("a deleted rule must ask again")
	}
}

// An agent's form request waits in the broker until the owner answers;
// a wrong answer is refused and the request stays open.
func TestElicitationBroker(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	asked := make(chan valueobject.Elicitation, 1)
	var resolved []string
	onEvent := func(ev event.TurnEvent) {
		switch ev.Kind() {
		case enum.TurnEventElicitation:
			asked <- ev.Elicitation()
		case enum.TurnEventElicitationResolved:
			resolved = append(resolved, ev.Delta())
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err := app.Send(ctx, "c_form", "", "", "fill the form", nil, appjob.SendOptions{Agent: "fake"}, onEvent)
		done <- err
	}()
	e := <-asked
	if p := app.PendingElicitations("c_form"); len(p) != 1 || p[0].ID() != e.ID() {
		t.Fatalf("pending = %+v", p)
	}
	bad := valueobject.ElicitationAnswer{Action: "accept", Content: []byte(`{"env":"staging"}`)}
	if err := app.AnswerElicitation("c_form", e.ID(), bad); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("an invalid choice = %v", err)
	}
	if err := app.AnswerElicitation("c_form", e.ID(), valueobject.ElicitationAnswer{Action: "maybe"}); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("an unknown action = %v", err)
	}
	if err := app.AnswerElicitation("c_elsewhere", e.ID(), valueobject.ElicitationAnswer{Action: "decline"}); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("another room = %v", err)
	}
	good := valueobject.ElicitationAnswer{Action: "accept", Content: []byte(`{"env":"dev"}`)}
	if err := app.AnswerElicitation("c_form", e.ID(), good); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(rt.answers) != 1 || string(rt.answers[0].Content) != `{"env":"dev"}` {
		t.Fatalf("agent got %+v", rt.answers)
	}
	if len(resolved) != 1 || resolved[0] != "accept" || len(app.PendingElicitations("c_form")) != 0 {
		t.Fatalf("resolved = %v", resolved)
	}
}

// Steering an agent that takes it mid-turn settles the receipt on
// delivery and needs no follow-up prompt; one that does not take it gets
// the input as a follow-up prompt after the turn.
func TestAgentSteering(t *testing.T) {
	for _, steerable := range []bool{true, false} {
		app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
		rt.steerable, rt.steered = steerable, make(chan string, 1)
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		mailbox := appjob.NewSteeringInput()
		var receipt *appjob.SteeringReceipt
		// Submitted while the agent's turn runs.
		onEvent := func(ev event.TurnEvent) {
			if ev.Kind() == enum.TurnEventToolCall && receipt == nil {
				receipt = mailbox.Submit(valueobject.UserInput{Text: "go left"})
			}
		}
		text := "wait for steer"
		if !steerable {
			text = "look"
		}
		done := make(chan error, 1)
		go func() {
			_, err := app.Send(ctx, "c_steer", "", "", text, nil, appjob.SendOptions{Agent: "fake", Steering: mailbox}, onEvent)
			done <- err
		}()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		cancel()
		if receipt.State() != appjob.SteeringPersisted {
			t.Fatalf("steerable=%v receipt = %s", steerable, receipt.State())
		}
		rt.mu.Lock()
		n := len(rt.prompts)
		rt.mu.Unlock()
		if steerable && n != 1 || !steerable && n != 2 {
			t.Fatalf("steerable=%v prompts = %d", steerable, n)
		}
	}
}
