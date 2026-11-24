package appconversation

import (
	"context"
	"fmt"
	"slices"
	"strings"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// The native command names. They are reserved words on a draft's first line:
// a name in the catalog always dispatches as a command, never as text.
const (
	CommandCompact = "compact"
	CommandClear   = "clear"
	CommandFork    = "fork"
)

// Command sources — who owns a catalog row.
const (
	CommandSourceNative = "native"
	CommandSourceAgent  = "agent"
)

// Why a native command is off. The composer shows the reason beside the row it
// disables, so each says what the owner would have to change.
const (
	reasonNoRoom        = "Send a message first"
	reasonBusy          = "Wait for the turn to finish"
	reasonAgentContext  = "The agent manages its own context"
	reasonNothingToFold = "Nothing to compact yet"
	reasonEmptyThread   = "This thread is empty"
	reasonNothingToFork = "Nothing to fork yet"
)

// Command is one row of the composer's slash-command catalog: a name, what it
// does, where it comes from, and this room's verdict on it.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Hint        string `json:"hint"`
	// Source is native for a command NusaShell runs itself, agent for one the
	// room's agent session offers.
	Source string `json:"source"`
	// Enabled and Reason are the server's verdict for this room. A refused row
	// stays visible: the reason says what would make it available.
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason"`
}

// nativeCommands is the whole native catalog, in the order the composer shows
// it. Every row is one action the application owns; none of them writes a
// message or reaches a model.
func nativeCommands() []Command {
	return []Command{
		{Name: CommandCompact, Description: "Summarize older history into a checkpoint", Source: CommandSourceNative},
		{Name: CommandClear, Description: "Empty this thread without changing what it is", Source: CommandSourceNative},
		{Name: CommandFork, Description: "Copy this thread into a new one", Source: CommandSourceNative},
	}
}

// UseLiveTurns sets the source of "a transport turn is live in this room",
// read on each command so a turn started anywhere — this browser, a chat, an
// automation — refuses history commands. Nil reads every room as idle, which
// is the right answer for a composition without the hub and for tests.
func (app *ConversationApp) UseLiveTurns(fn func(convID string) bool) {
	app.mu.Lock()
	app.liveTurns = fn
	app.mu.Unlock()
}

// busy reports whether a transport turn is live in the room.
func (app *ConversationApp) busy(convID string) bool {
	app.mu.Lock()
	fn := app.liveTurns
	app.mu.Unlock()
	return fn != nil && fn(convID)
}

// Commands is the room's slash-command catalog: the native commands, then the
// room's agent commands, one row per name — native wins a collision — each
// with this room's availability. Without a conversation id it answers the
// native set with every row off: a composer with no room yet.
func (app *ConversationApp) Commands(ctx context.Context, convID string) ([]Command, error) {
	rows := nativeCommands()
	if convID == "" {
		for i := range rows {
			rows[i].Enabled, rows[i].Reason = false, reasonNoRoom
		}
		return rows, nil
	}
	deps := app.depsSnapshot()
	info, err := deps.Convs.Info(ctx, convID)
	if err != nil {
		return nil, err
	}
	busy := app.busy(convID)
	// Foldability is the aggregate's decision; ask it once, unless the room
	// cannot fold at all (a live turn, an agent room).
	canFold := false
	if !busy && info.AgentID == "" {
		if canFold, err = app.CanCompact(ctx, convID); err != nil {
			return nil, err
		}
	}
	for i, row := range rows {
		rows[i].Enabled, rows[i].Reason = availability(row.Name, info, busy, canFold)
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		seen[row.Name] = true
	}
	for _, c := range app.AgentCommands(convID) {
		if c.Name == "" || seen[c.Name] {
			continue
		}
		seen[c.Name] = true
		rows = append(rows, Command{Name: c.Name, Description: c.Description, Hint: c.Hint, Source: CommandSourceAgent, Enabled: true})
	}
	return rows, nil
}

// availability is the server's verdict on one native command in one room. The
// order is the reason that matters most: a live turn refuses everything, an
// agent room has no foldable tail of ours, and an empty room has nothing to act
// on.
func availability(name string, info drepo.ConversationInfo, busy, canFold bool) (bool, string) {
	switch {
	case busy:
		return false, reasonBusy
	case name == CommandCompact && info.AgentID != "":
		return false, reasonAgentContext
	case info.MessageCount == 0:
		return false, emptyReason(name)
	case name == CommandCompact && !canFold:
		return false, reasonNothingToFold
	}
	return true, ""
}

// emptyReason names why an empty room offers nothing for one command.
func emptyReason(name string) string {
	switch name {
	case CommandClear:
		return reasonEmptyThread
	case CommandFork:
		return reasonNothingToFork
	}
	return reasonNothingToFold
}

// CommandInput is one command invocation, as a composer or a chat ingress
// sends it: the catalog name, its arguments, and the confirmation a
// destructive command needs.
type CommandInput struct {
	Name    string
	Args    string
	Confirm bool
}

// CommandResult is what a command did: the room the caller is left on — the
// new room for a fork.
type CommandResult struct {
	Command      string
	Conversation drepo.ConversationInfo
}

// RunCommand executes one native command. A command is an action, not a turn:
// it writes no message and emits no turn frame. Every precondition lives here —
// the room must be idle, the command must apply to its kind, the room must hold
// something to act on, and a destructive command must be confirmed — so a chat
// ingress reuses the same rules instead of restating them.
func (app *ConversationApp) RunCommand(ctx context.Context, convID string, in CommandInput) (CommandResult, error) {
	name := strings.TrimSpace(in.Name)
	if !slices.ContainsFunc(nativeCommands(), func(c Command) bool { return c.Name == name }) {
		return CommandResult{}, apperror.NewCoded(400, "invalid_argument", "unknown command", fmt.Errorf("no native command named %q", in.Name))
	}
	// A command's arguments are fixed in this version: clear takes the word
	// confirm, the others take nothing. A recognized command with the wrong
	// arguments is refused, never downgraded to text.
	confirmed := in.Confirm
	switch args := strings.Fields(in.Args); name {
	case CommandClear:
		for _, arg := range args {
			if arg != "confirm" {
				return CommandResult{}, apperror.NewCoded(400, "invalid_argument", fmt.Sprintf("clear takes no argument %q", arg), nil)
			}
			confirmed = true
		}
	case CommandCompact, CommandFork:
		if len(args) > 0 {
			return CommandResult{}, apperror.NewCoded(400, "invalid_argument", name+" takes no arguments", nil)
		}
	}

	deps := app.depsSnapshot()
	info, err := deps.Convs.Info(ctx, convID)
	if err != nil {
		return CommandResult{}, err
	}
	if app.busy(convID) {
		return CommandResult{}, apperror.NewCoded(409, "busy", reasonBusy, nil)
	}
	switch name {
	case CommandCompact:
		if info.AgentID != "" {
			return CommandResult{}, apperror.NewCoded(409, "unavailable", reasonAgentContext, nil)
		}
		canFold, err := app.CanCompact(ctx, convID)
		if err != nil {
			return CommandResult{}, err
		}
		if !canFold {
			return CommandResult{}, apperror.NewCoded(400, "nothing_to_compact", reasonNothingToFold, nil)
		}
		if err := app.Compact(ctx, convID); err != nil {
			return CommandResult{}, err
		}
		if info, err = deps.Convs.Info(ctx, convID); err != nil {
			return CommandResult{}, err
		}
		app.notifyChanged(convID)
	case CommandClear:
		if !confirmed {
			return CommandResult{}, apperror.NewCoded(409, "confirmation_required", "nothing is cleared without confirm", nil)
		}
		if info.MessageCount == 0 {
			return CommandResult{}, apperror.NewCoded(400, "nothing_to_clear", reasonEmptyThread, nil)
		}
		if err := app.Clear(ctx, convID); err != nil {
			return CommandResult{}, err
		}
		if info, err = deps.Convs.Info(ctx, convID); err != nil {
			return CommandResult{}, err
		}
		app.notifyChanged(convID)
	case CommandFork:
		if info.MessageCount == 0 {
			return CommandResult{}, apperror.NewCoded(400, "nothing_to_fork", reasonNothingToFork, nil)
		}
		conv, err := deps.Convs.Load(ctx, convID)
		if err != nil {
			return CommandResult{}, err
		}
		dst, err := conv.Fork()
		if err != nil {
			return CommandResult{}, err
		}
		if err := app.Fork(ctx, convID, dst); err != nil {
			return CommandResult{}, err
		}
		if info, err = deps.Convs.Info(ctx, dst.ID()); err != nil {
			return CommandResult{}, err
		}
		// The copy is a new room every tab's list lacks; its projection rides
		// the answer, and the notice tells the others to re-read.
		app.notifyChanged(dst.ID())
	}
	return CommandResult{Command: name, Conversation: info}, nil
}

// Clear empties the room's history and tool output on the room's worker —
// queued like a send, so a turn already running finishes first — and keeps the
// room's identity.
func (app *ConversationApp) Clear(ctx context.Context, convID string) error {
	res := make(chan error, 1)
	j := &appjob.Clear{Ctx: ctx}
	j.Result = res
	app.post(convID, j)
	select {
	case err := <-res:
		return err
	case <-ctx.Done():
		return apperror.New(408, "clear request canceled", ctx.Err())
	}
}

// Fork copies the room's history into dst on the room's worker, so the copy
// reads a history no turn is writing.
func (app *ConversationApp) Fork(ctx context.Context, convID string, dst *aggregate.Conversation) error {
	res := make(chan error, 1)
	j := &appjob.Fork{Ctx: ctx, Dst: dst}
	j.Result = res
	app.post(convID, j)
	select {
	case err := <-res:
		return err
	case <-ctx.Done():
		return apperror.New(408, "fork request canceled", ctx.Err())
	}
}
