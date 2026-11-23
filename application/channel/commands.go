package appchannel

import (
	"context"
	"fmt"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

const helpText = `NusaShell commands:
/new — start a new thread for this chat
/use — list models and agents; /use <name>[/<model>] switches the next reply
/room — show this chat's thread; /room @label links this chat to another thread
/status — show the thread, its model or agent, and whether it is working
/stop — stop the running reply
/approve [id] · /deny [id] — answer the agent's permission requests
Anything else goes to the thread.`

// parseCommand splits "/cmd@bot arg1 arg2" into its lowercased name and
// arguments. ok is false for text that is not a command.
func parseCommand(text string) (cmd string, args []string, ok bool) {
	if !strings.HasPrefix(text, "/") {
		return "", nil, false
	}
	fields := strings.Fields(text)
	cmd = strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	if at := strings.IndexByte(cmd, '@'); at >= 0 {
		cmd = cmd[:at] // Telegram appends the bot name in groups
	}
	return cmd, fields[1:], cmd != ""
}

// command runs one of this package's commands. It reports false for a
// command it does not know, which then reaches the room as text — agents
// have slash commands of their own.
func (a *App) command(ctx context.Context, in dservice.InboundMessage, convID, cmd string, args []string) bool {
	chat := in.Chat
	switch cmd {
	case "help", "start":
		a.reply(ctx, chat, helpText)
	case "new":
		if convID != "" {
			a.runner.Cancel(convID)
		}
		if err := a.links.Delete(ctx, chat); err != nil {
			a.reply(ctx, chat, "Could not start over: "+err.Error())
			return true
		}
		a.syncMenu(ctx, chat, nil)
		a.reply(ctx, chat, "OK — your next message starts a new thread. The old one stays on the web.")
	case "stop", "cancel":
		if convID == "" || !a.runner.Cancel(convID) {
			a.reply(ctx, chat, "Nothing is running.")
			return true
		}
		a.reply(ctx, chat, "Stopping…")
	case "status":
		a.status(ctx, chat, convID)
	case "room":
		a.room(ctx, in, convID, args)
	case "use":
		a.use(ctx, chat, convID, args)
	case "approve", "deny":
		a.approval(ctx, chat, convID, cmd == "approve", args)
	default:
		return false
	}
	return true
}

func (a *App) status(ctx context.Context, chat valueobject.ChatRef, convID string) {
	if convID == "" {
		a.reply(ctx, chat, "This chat has no thread yet — send a message to start one.")
		return
	}
	info, err := a.rooms.Info(ctx, convID)
	if err != nil {
		a.reply(ctx, chat, "This chat's thread is not there anymore. Send a message to start a new one, or /new.")
		return
	}
	rt := Runtime{AgentID: info.AgentID, ProviderID: info.ProviderID, ModelID: info.ModelID}
	state := "idle"
	if a.runner.Busy(convID) {
		state = "working"
	}
	lines := []string{"Thread: " + firstNonEmpty(info.Title, "Untitled")}
	if info.Label != "" {
		lines = append(lines, "Label: @"+info.Label)
	}
	if !rt.empty() {
		lines = append(lines, "Runs on: "+rt.String())
	}
	if next, ok := a.peekNext(convID); ok {
		lines = append(lines, "Next reply: "+next.String())
	}
	lines = append(lines, "State: "+state)
	a.reply(ctx, chat, strings.Join(lines, "\n"))
}

// room shows or switches the thread this chat talks to.
func (a *App) room(ctx context.Context, in dservice.InboundMessage, convID string, args []string) {
	chat := in.Chat
	if len(args) == 0 {
		if convID == "" {
			a.reply(ctx, chat, "This chat has no thread yet. /room @label links it to an existing one.")
			return
		}
		info, err := a.rooms.Info(ctx, convID)
		if err != nil {
			a.reply(ctx, chat, "This chat's thread is not there anymore.")
			return
		}
		label := "(no label)"
		if info.Label != "" {
			label = "@" + info.Label
		}
		a.reply(ctx, chat, fmt.Sprintf("This chat talks to %s — %s.", label, firstNonEmpty(info.Title, "Untitled")))
		return
	}
	want := valueobject.NormalizeLabel(args[0])
	infos, err := a.rooms.List(ctx)
	if err != nil {
		a.reply(ctx, chat, "Could not look up threads: "+err.Error())
		return
	}
	for _, info := range infos {
		if info.Label != "" && info.Label == want {
			if err := a.link(ctx, in, info.ID); err != nil {
				a.reply(ctx, chat, "Could not link this chat: "+err.Error())
				return
			}
			a.syncMenu(ctx, chat, a.rooms.AgentCommands(info.ID))
			a.reply(ctx, chat, fmt.Sprintf("This chat now talks to @%s — %s.", info.Label, firstNonEmpty(info.Title, "Untitled")))
			return
		}
	}
	a.reply(ctx, chat, fmt.Sprintf("No thread is labeled @%s. Labels are shown next to each thread's title on the web.", want))
}

// use lists runtimes, or picks one for the room's next reply.
func (a *App) use(ctx context.Context, chat valueobject.ChatRef, convID string, args []string) {
	choices, err := a.catalog.Choices(ctx)
	if err != nil {
		a.reply(ctx, chat, "Could not list models: "+err.Error())
		return
	}
	if len(args) == 0 {
		if len(choices) == 0 {
			a.reply(ctx, chat, "No model or agent is set up yet.")
			return
		}
		lines := []string{"Pick one with /use <name>:"}
		for _, c := range choices {
			lines = append(lines, "• "+useName(c.Runtime)+" — "+c.Name)
		}
		a.reply(ctx, chat, strings.Join(lines, "\n"))
		return
	}
	want := strings.ToLower(args[0])
	for _, c := range choices {
		if strings.ToLower(useName(c.Runtime)) != want && !(c.Runtime.AgentID != "" && strings.ToLower(c.Runtime.AgentID) == want) {
			continue
		}
		key := convID
		if key == "" {
			key = "chat:" + chat.Key()
		}
		a.mu.Lock()
		a.next[key] = c.Runtime
		a.mu.Unlock()
		a.reply(ctx, chat, "OK — the next reply runs on "+c.Runtime.String()+".")
		return
	}
	a.reply(ctx, chat, fmt.Sprintf("Nothing is called %q. Send /use to see the list.", args[0]))
}

// useName is how a runtime is typed after /use: the agent id, the agent id
// and model, or provider/model.
func useName(r Runtime) string {
	switch {
	case r.AgentID != "" && r.ModelID != "":
		return r.AgentID + "/" + r.ModelID
	case r.AgentID != "":
		return r.AgentID
	}
	return r.ProviderID + "/" + r.ModelID
}

func (a *App) peekNext(convID string) (Runtime, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rt, ok := a.next[convID]
	return rt, ok
}

// approval lists the room's open permission requests, or answers one by
// the prefix of its id.
func (a *App) approval(ctx context.Context, chat valueobject.ChatRef, convID string, allow bool, args []string) {
	if convID == "" {
		a.reply(ctx, chat, "This chat has no thread yet.")
		return
	}
	pending := a.rooms.PendingApprovals(convID)
	if len(pending) == 0 {
		a.reply(ctx, chat, "Nothing is waiting for an answer.")
		return
	}
	verb := "/deny"
	if allow {
		verb = "/approve"
	}
	if len(args) == 0 {
		if len(pending) == 1 {
			args = []string{shortID(pending[0].ID())}
		} else {
			lines := []string{"Waiting for an answer:"}
			for _, r := range pending {
				lines = append(lines, fmt.Sprintf("• %s — %s", shortID(r.ID()), firstNonEmpty(r.Title(), r.Action().String())))
			}
			lines = append(lines, "Reply "+verb+" <id>.")
			a.reply(ctx, chat, strings.Join(lines, "\n"))
			return
		}
	}
	prefix := strings.ToLower(args[0])
	var match []valueobject.ApprovalRequest
	for _, r := range pending {
		if strings.HasPrefix(shortIDFull(r.ID()), prefix) {
			match = append(match, r)
		}
	}
	if len(match) != 1 {
		a.reply(ctx, chat, fmt.Sprintf("%q matches %d requests. Send %s alone to list them.", prefix, len(match), verb))
		return
	}
	r := match[0]
	want := []enum.ApprovalChoice{enum.ApprovalRejectOnce, enum.ApprovalRejectAlways}
	if allow {
		want = []enum.ApprovalChoice{enum.ApprovalAllowOnce, enum.ApprovalAllowAlways}
	}
	for _, choice := range want {
		for _, o := range r.Options() {
			if o.Choice != choice {
				continue
			}
			if err := a.rooms.Approve(convID, r.ID(), o.ID); err != nil {
				a.reply(ctx, chat, "Could not answer: "+err.Error())
				return
			}
			a.reply(ctx, chat, "Answered: "+o.Name+".")
			return
		}
	}
	a.reply(ctx, chat, "That request offers no such answer; answer it on the web.")
}

func shortIDFull(id string) string { return strings.ToLower(strings.TrimPrefix(id, "apr_")) }

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
