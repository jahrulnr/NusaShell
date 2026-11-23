package appchannel

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

const (
	// pollMaxOptions is WhatsApp's cap; a longer list stays a text ping.
	pollMaxOptions  = 12
	pollOptionMax   = 90
	pollQuestionMax = 200
	// pollMultiQuiet is how long a multi-answer poll waits after the last
	// tap: each tap sends the whole selection again.
	pollMultiQuiet = 15 * time.Second
)

// pollWaiter ties a poll a chat was sent to the request it answers: an
// approval or a one-question elicitation of the room's running turn.
type pollWaiter struct {
	convID string
	chat   valueobject.ChatRef
	sender string // who may answer: the sender of the turn's message

	approval    string // the approval request's id, or
	elicitation string // the elicitation's id, with question
	question    valueobject.ElicitationChoice

	options map[string]string // poll label → option id or answer value
	multi   bool
	latest  []string
	gen     uint64
	timer   *time.Timer
}

func pollKey(channel, pollID string) string { return channel + "|" + pollID }

// askApproval brings a permission request to the chat: as a poll when the
// chat has native polls, else as a text ping answered with /approve.
func (a *App) askApproval(ctx context.Context, chat valueobject.ChatRef, sender, convID string, r valueobject.ApprovalRequest) {
	options := map[string]string{}
	var labels []string
	for _, o := range r.Options() {
		label := pollLabel(firstNonEmpty(o.Name, o.ID), options)
		options[label] = o.ID
		labels = append(labels, label)
	}
	what := firstNonEmpty(r.Title(), r.Action().String())
	question := "🔐 The agent asks to run: " + what + "\n(or /approve " + shortID(r.ID()) + " · /deny " + shortID(r.ID()) + ")"
	w := &pollWaiter{convID: convID, chat: chat, sender: sender, approval: r.ID(), options: options}
	if !a.poll(ctx, w, question, labels, 1) {
		a.reply(ctx, chat, approvalPing(r))
	}
}

// askElicitation brings the agent's question to the chat: a poll for a
// one-question choice when the chat has polls, else a pointer to the web.
func (a *App) askElicitation(ctx context.Context, chat valueobject.ChatRef, sender, convID string, e valueobject.Elicitation) {
	question := firstNonEmpty(e.Message(), "The agent has a question.")
	if c, ok := e.Choice(); ok {
		options := map[string]string{}
		labels := make([]string, 0, len(c.Options))
		for _, o := range c.Options {
			label := pollLabel(o, options)
			options[label] = o
			labels = append(labels, label)
		}
		w := &pollWaiter{convID: convID, chat: chat, sender: sender, elicitation: e.ID(), question: c,
			options: options, multi: c.Max > 1}
		if a.poll(ctx, w, question, labels, c.Max) {
			return
		}
	}
	a.reply(ctx, chat, "❓ "+question+"\nAnswer it on the web.")
}

// poll sends the poll and keeps its waiter; false when the chat has no
// polls, the options do not fit one, or sending failed.
func (a *App) poll(ctx context.Context, w *pollWaiter, question string, labels []string, maxSelectable int) bool {
	ch, ok := a.Channel(w.chat.Channel())
	if !ok {
		return false
	}
	ps, ok := ch.(dservice.PollSender)
	if !ok || len(labels) < 2 || len(labels) > pollMaxOptions {
		return false
	}
	id, err := ps.SendPoll(ctx, w.chat, truncateRunes(question, pollQuestionMax), labels, maxSelectable)
	if err != nil {
		a.log.Warn("could not send a poll", "channel", w.chat.Channel(), "error", err)
		return false
	}
	a.mu.Lock()
	a.polls[pollKey(w.chat.Channel(), id)] = w
	a.mu.Unlock()
	return true
}

// dropPolls forgets the waiters drop picks: their request was answered
// elsewhere, or their turn ended.
func (a *App) dropPolls(drop func(*pollWaiter) bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, w := range a.polls {
		if drop(w) {
			if w.timer != nil {
				w.timer.Stop()
			}
			delete(a.polls, k)
		}
	}
}

// vote takes a vote on a poll this app sent. Only the person whose message
// started the turn answers it; a multi-answer poll settles after a quiet
// spell.
func (a *App) vote(ctx context.Context, in dservice.InboundMessage) {
	key := pollKey(in.Chat.Channel(), in.Vote.PollID)
	a.mu.Lock()
	w := a.polls[key]
	a.mu.Unlock()
	if w == nil {
		a.reply(ctx, in.Chat, "That request has already ended.")
		return
	}
	if in.SenderID != w.sender {
		// In a group the asker still answers; replying there would only
		// echo other people's taps.
		if !in.Group {
			a.reply(ctx, in.Chat, "Only the person who asked can answer that.")
		}
		return
	}
	if in.Vote.Selected == nil {
		a.reply(ctx, in.Chat, "That vote could not be read. Vote again or answer on the web.")
		return
	}
	for _, s := range in.Vote.Selected {
		if _, ok := w.options[s]; !ok {
			return
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.polls[key] != w {
		return
	}
	if !w.multi {
		if len(in.Vote.Selected) == 0 {
			return // a vote taken back
		}
		delete(a.polls, key)
		a.wg.Go(func() { a.settle(in.Chat, w, in.Vote.Selected) })
		return
	}
	w.latest = in.Vote.Selected
	w.gen++
	gen := w.gen
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(pollMultiQuiet, func() {
		a.mu.Lock()
		if a.polls[key] != w || w.gen != gen || len(w.latest) == 0 {
			a.mu.Unlock()
			return
		}
		delete(a.polls, key)
		picks := w.latest
		a.mu.Unlock()
		a.settle(in.Chat, w, picks)
	})
}

// settle answers the request with the picked poll labels.
func (a *App) settle(chat valueobject.ChatRef, w *pollWaiter, picks []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var err error
	if w.approval != "" {
		err = a.rooms.Approve(w.convID, w.approval, w.options[picks[0]])
	} else {
		values := make([]string, 0, len(picks))
		for _, p := range picks {
			values = append(values, w.options[p])
		}
		var ans valueobject.ElicitationAnswer
		if ans, err = w.question.Answer(values); err == nil {
			err = a.rooms.AnswerElicitation(w.convID, w.elicitation, ans)
		}
	}
	if err != nil {
		a.reply(ctx, chat, "Could not answer: "+err.Error())
		return
	}
	a.reply(ctx, chat, "Answered: "+strings.Join(picks, ", ")+".")
}

// pollLabel fits an option to a poll and keeps it unique, so a vote maps
// back to exactly one answer.
func pollLabel(text string, taken map[string]string) string {
	base := truncateRunes(strings.TrimSpace(text), pollOptionMax)
	label := base
	for i := 2; ; i++ {
		if _, ok := taken[label]; !ok {
			return label
		}
		label = fmt.Sprintf("%s (%d)", base, i)
	}
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
