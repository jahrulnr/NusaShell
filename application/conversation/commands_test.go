package appconversation

import (
	"context"
	"testing"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// row answers one catalog row by name.
func row(t *testing.T, rows []Command, name string) Command {
	t.Helper()
	for _, r := range rows {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("catalog has no %q: %+v", name, rows)
	return Command{}
}

// seedRoom sends one exchange into a fresh room and answers its id.
func seedRoom(t *testing.T, app *ConversationApp) string {
	t.Helper()
	res, err := app.Send(t.Context(), "", "gw1", "m1", "hello there", nil, appjob.SendOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.ConversationID
}

// The catalog follows the availability matrix: no room, a live turn, an agent
// room, an empty room, and a room with nothing foldable each carry their own
// reason.
func TestCommandCatalogMatrix(t *testing.T) {
	app, _ := fixture(t, okHandler)

	// A composer with no room yet: the names are reserved, nothing runs.
	rows, err := app.Commands(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("native catalog = %+v", rows)
	}
	for _, r := range rows {
		if r.Source != CommandSourceNative || r.Enabled || r.Reason != reasonNoRoom {
			t.Fatalf("fresh composer row = %+v", r)
		}
	}

	// An empty room has nothing to act on, and says which nothing.
	empty, err := aggregate.NewConversation("")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.deps.Convs.Save(t.Context(), empty); err != nil {
		t.Fatal(err)
	}
	rows, err = app.Commands(t.Context(), empty.ID())
	if err != nil {
		t.Fatal(err)
	}
	if r := row(t, rows, CommandCompact); r.Enabled || r.Reason != reasonNothingToFold {
		t.Fatalf("empty room compact = %+v", r)
	}
	if r := row(t, rows, CommandClear); r.Enabled || r.Reason != reasonEmptyThread {
		t.Fatalf("empty room clear = %+v", r)
	}
	if r := row(t, rows, CommandFork); r.Enabled || r.Reason != reasonNothingToFork {
		t.Fatalf("empty room fork = %+v", r)
	}

	// A room with history offers clear and fork at once; compact still needs a
	// tail longer than the keep-tail.
	room := seedRoom(t, app)
	rows, err = app.Commands(t.Context(), room)
	if err != nil {
		t.Fatal(err)
	}
	if r := row(t, rows, CommandClear); !r.Enabled || r.Reason != "" {
		t.Fatalf("history room clear = %+v", r)
	}
	if r := row(t, rows, CommandFork); !r.Enabled || r.Reason != "" {
		t.Fatalf("history room fork = %+v", r)
	}
	if r := row(t, rows, CommandCompact); r.Enabled || r.Reason != reasonNothingToFold {
		t.Fatalf("short tail compact = %+v", r)
	}

	// A live transport turn refuses everything until it finishes.
	app.UseLiveTurns(func(string) bool { return true })
	rows, err = app.Commands(t.Context(), room)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{CommandCompact, CommandClear, CommandFork} {
		if r := row(t, rows, name); r.Enabled || r.Reason != reasonBusy {
			t.Fatalf("busy room %s = %+v", name, r)
		}
	}
	app.UseLiveTurns(nil)

	// An agent room without an agent-reported compact has no compact row at
	// all: native compaction belongs to harness rooms only.
	agentRoom := seedAgentCommandRoom(t, app)
	rows, err = app.Commands(t.Context(), agentRoom)
	if err != nil {
		t.Fatal(err)
	}
	if hasCommand(rows, CommandCompact) {
		t.Fatalf("an agent room without the agent command still carries compact: %+v", rows)
	}
	if r := row(t, rows, CommandClear); !r.Enabled {
		t.Fatalf("agent room clear = %+v", r)
	}
	if r := row(t, rows, CommandFork); !r.Enabled {
		t.Fatalf("agent room fork = %+v", r)
	}
}

type commandListingRuntime struct {
	dservice.AgentRuntime
	commands []dservice.AgentCommand
}

func (r commandListingRuntime) Commands(context.Context, string, string) []dservice.AgentCommand {
	return append([]dservice.AgentCommand(nil), r.commands...)
}

func seedAgentCommandRoom(t *testing.T, app *ConversationApp) string {
	t.Helper()
	room, err := aggregate.NewConversation("")
	if err != nil {
		t.Fatal(err)
	}
	if err := room.BindAgent("a_external"); err != nil {
		t.Fatal(err)
	}
	room.SetTitle("agent thread")
	if err := app.deps.Convs.Save(t.Context(), room); err != nil {
		t.Fatal(err)
	}
	if _, err := app.deps.Convs.AppendMessage(t.Context(), room.ID(), userMessage(t, "hi")); err != nil {
		t.Fatal(err)
	}
	return room.ID()
}

func hasCommand(rows []Command, name string) bool {
	for _, r := range rows {
		if r.Name == name {
			return true
		}
	}
	return false
}

// A compact command in an agent room belongs to the agent's own catalog:
// present and enabled when the session offers it, absent when it does not,
// and never a disabled native row claiming what the agent manages.
func TestAgentCompactFollowsTheAgentCatalog(t *testing.T) {
	app, _ := fixture(t, okHandler)
	room := seedAgentCommandRoom(t, app)

	app.deps.AgentRuntime = commandListingRuntime{}
	rows, err := app.Commands(t.Context(), room)
	if err != nil {
		t.Fatal(err)
	}
	if hasCommand(rows, CommandCompact) {
		t.Fatalf("agent without compact still has a row: %+v", rows)
	}

	app.deps.AgentRuntime = commandListingRuntime{commands: []dservice.AgentCommand{{Name: CommandCompact, Description: "Compact the agent session"}}}
	rows, err = app.Commands(t.Context(), room)
	if err != nil {
		t.Fatal(err)
	}
	r := row(t, rows, CommandCompact)
	if r.Source != CommandSourceAgent || !r.Enabled || r.Reason != "" {
		t.Fatalf("agent compact row = %+v", r)
	}

	app.UseLiveTurns(func(string) bool { return true })
	defer app.UseLiveTurns(nil)
	rows, err = app.Commands(t.Context(), room)
	if err != nil {
		t.Fatal(err)
	}
	r = row(t, rows, CommandCompact)
	if r.Enabled || r.Reason != reasonBusy {
		t.Fatalf("busy agent compact row = %+v", r)
	}
}

// The proxy rule is general: the catalog carries only the thread commands we
// own plus the commands the session reported — never an agent-looking row we
// invented — and when the agent claims a colliding name, its row wins.
func TestAgentCommandCatalogMirrorsOnlyWhatTheSessionReports(t *testing.T) {
	app, _ := fixture(t, okHandler)
	room := seedAgentCommandRoom(t, app)
	app.deps.AgentRuntime = commandListingRuntime{commands: []dservice.AgentCommand{
		{Name: "review", Description: "Review the working tree"},
		{Name: CommandClear, Description: "The agent's own clear"},
	}}
	rows, err := app.Commands(t.Context(), room)
	if err != nil {
		t.Fatal(err)
	}
	if hasCommand(rows, CommandCompact) {
		t.Fatalf("an unreported compact was invented: %+v", rows)
	}
	if r := row(t, rows, CommandClear); r.Source != CommandSourceAgent || r.Description != "The agent's own clear" {
		t.Fatalf("colliding clear must be the agent's row: %+v", rows)
	}
	if r := row(t, rows, CommandFork); r.Source != CommandSourceNative {
		t.Fatalf("unclaimed fork must remain the thread command: %+v", rows)
	}
	if r := row(t, rows, "review"); r.Source != CommandSourceAgent {
		t.Fatalf("reported review missing: %+v", rows)
	}
	for _, r := range rows {
		if r.Source == CommandSourceAgent && r.Name != "review" && r.Name != CommandClear {
			t.Fatalf("unreported agent command invented: %+v", rows)
		}
	}
}

// Thread commands are still real actions in an agent room: they operate on
// NusaShell's own history even though context commands belong to the agent.
func TestAgentRoomThreadCommandsStillRun(t *testing.T) {
	app, _ := fixture(t, okHandler)
	room := seedAgentCommandRoom(t, app)

	res, err := app.RunCommand(t.Context(), room, CommandInput{Name: CommandClear, Confirm: true})
	if err != nil {
		t.Fatalf("clear in an agent room: %v", err)
	}
	if res.Conversation.MessageCount != 0 {
		t.Fatalf("clear left history behind: %+v", res.Conversation)
	}

	if _, err := app.deps.Convs.AppendMessage(t.Context(), room, userMessage(t, "copy me")); err != nil {
		t.Fatal(err)
	}
	res, err = app.RunCommand(t.Context(), room, CommandInput{Name: CommandFork})
	if err != nil {
		t.Fatalf("fork in an agent room: %v", err)
	}
	if res.Conversation.ID == room || res.Conversation.MessageCount == 0 || res.Conversation.AgentID != "a_external" {
		t.Fatalf("fork result = %+v", res.Conversation)
	}
}

// A refused command names its own contract code: unknown names and stray
// arguments are invalid arguments, a missing confirmation is its own 409, and
// an empty room has nothing to act on.
func TestRunCommandRefusals(t *testing.T) {
	app, _ := fixture(t, okHandler)
	room := seedRoom(t, app)
	empty, err := aggregate.NewConversation("")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.deps.Convs.Save(t.Context(), empty); err != nil {
		t.Fatal(err)
	}

	agentRoom, err := aggregate.NewConversation("")
	if err != nil {
		t.Fatal(err)
	}
	if err := agentRoom.BindAgent("a_external"); err != nil {
		t.Fatal(err)
	}
	if err := app.deps.Convs.Save(t.Context(), agentRoom); err != nil {
		t.Fatal(err)
	}
	if _, err := app.deps.Convs.AppendMessage(t.Context(), agentRoom.ID(), userMessage(t, "hi")); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		conv  string
		in    CommandInput
		code  string
		state int
	}{
		{"unknown", room, CommandInput{Name: "rewind"}, "invalid_argument", 400},
		{"lowercase match", room, CommandInput{Name: "Compact"}, "invalid_argument", 400},
		{"compact args", room, CommandInput{Name: CommandCompact, Args: "now"}, "invalid_argument", 400},
		{"fork args", room, CommandInput{Name: CommandFork, Args: "2"}, "invalid_argument", 400},
		{"clear stray arg", room, CommandInput{Name: CommandClear, Args: "yes"}, "invalid_argument", 400},
		{"clear unconfirmed", room, CommandInput{Name: CommandClear}, "confirmation_required", 409},
		{"clear empty", empty.ID(), CommandInput{Name: CommandClear, Confirm: true}, "nothing_to_clear", 400},
		{"fork empty", empty.ID(), CommandInput{Name: CommandFork}, "nothing_to_fork", 400},
		{"compact empty", empty.ID(), CommandInput{Name: CommandCompact}, "nothing_to_compact", 400},
		{"compact agent room", agentRoom.ID(), CommandInput{Name: CommandCompact}, "unavailable", 409},
		{"missing room", "c_missing", CommandInput{Name: CommandFork}, "", 404}, // 404 derives its code
	} {
		_, err := app.RunCommand(t.Context(), tc.conv, tc.in)
		ae := apperror.As(err)
		if ae.StatusCode() != tc.state || (tc.code != "" && ae.Code() != tc.code) {
			t.Fatalf("%s: got %d %q (%v), want %d %q", tc.name, ae.StatusCode(), ae.Code(), err, tc.state, tc.code)
		}
	}

	// A live turn refuses every command with its own 409.
	app.UseLiveTurns(func(string) bool { return true })
	defer app.UseLiveTurns(nil)
	for _, name := range []string{CommandCompact, CommandClear, CommandFork} {
		_, err := app.RunCommand(t.Context(), room, CommandInput{Name: name, Confirm: true})
		if ae := apperror.As(err); ae.StatusCode() != 409 || ae.Code() != "busy" {
			t.Fatalf("busy %s: %v", name, err)
		}
	}
}

// /clear empties the room but keeps what the room is: title, label, bindings,
// and usage survive; the messages, folds, and tool output do not. The ingress
// spelling — the word confirm as an argument — works as well as the flag.
func TestRunCommandClearKeepsIdentity(t *testing.T) {
	app, _ := fixture(t, okHandler)
	room := seedRoom(t, app)
	before, err := app.Info(t.Context(), room)
	if err != nil {
		t.Fatal(err)
	}
	var changed []string
	app.OnChanged(func(id string) { changed = append(changed, id) })

	res, err := app.RunCommand(t.Context(), room, CommandInput{Name: CommandClear, Args: "confirm"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Command != CommandClear || res.Conversation.ID != room {
		t.Fatalf("clear result = %+v", res)
	}
	after := res.Conversation
	if after.MessageCount != 0 || after.ActiveCount != 0 || after.SealedSegments != 0 {
		t.Fatalf("counters after clear = %+v", after)
	}
	if after.Title != before.Title || after.ProviderID != before.ProviderID || after.ModelID != before.ModelID ||
		after.Label != before.Label || after.ProjectID != before.ProjectID {
		t.Fatalf("identity changed: before %+v after %+v", before, after)
	}
	if n := len(changed); n != 1 || changed[0] != room {
		t.Fatalf("changed notices = %v", changed)
	}
}

// /fork answers the copy, not the source: the copy carries the whole history
// and the source stays as it was. A named room's copy carries the marker.
func TestRunCommandForkCopies(t *testing.T) {
	app, _ := fixture(t, okHandler)
	room := seedRoom(t, app)
	title := "release notes"
	if _, err := app.Annotate(t.Context(), room, Annotation{Title: &title}); err != nil {
		t.Fatal(err)
	}
	source, err := app.Info(t.Context(), room)
	if err != nil {
		t.Fatal(err)
	}

	res, err := app.RunCommand(t.Context(), room, CommandInput{Name: CommandFork})
	if err != nil {
		t.Fatal(err)
	}
	fork := res.Conversation
	if res.Command != CommandFork || fork.ID == room {
		t.Fatalf("fork result = %+v", res)
	}
	if fork.Title != title+" (fork)" {
		t.Fatalf("fork title = %q, want %q", fork.Title, title+" (fork)")
	}
	if fork.MessageCount != source.MessageCount || fork.ActiveCount != source.ActiveCount {
		t.Fatalf("fork counters = %+v, source = %+v", fork, source)
	}
	if fork.Label != "" || fork.Pinned || fork.AgentSession != "" {
		t.Fatalf("fork inherited a per-room binding: %+v", fork)
	}
	history, err := app.History(t.Context(), fork.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != source.MessageCount {
		t.Fatalf("fork history = %d messages, want %d", len(history), source.MessageCount)
	}
	still, err := app.Info(t.Context(), room)
	if err != nil {
		t.Fatal(err)
	}
	if still.MessageCount != source.MessageCount || still.Title != title {
		t.Fatalf("source changed: %+v", still)
	}
}

// userMessage builds a plain user message for a seeded room.
func userMessage(t *testing.T, text string) entity.Message {
	t.Helper()
	m, err := entity.NewMessage(enum.RoleUser, text)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
