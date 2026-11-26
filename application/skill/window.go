package skill

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// digestClip bounds the model's own text in the digest. Owner lines stay verbatim.
const digestClip = 600

// Window is the turn the signals and the log read: its opening owner line and
// every non-system message after it, in order.
type Window struct {
	AnchorID string
	Msgs     []entity.Message
	Owner    string // newest owner line of the window, verbatim
	Digest   string // Msgs rendered as text, the basis of the size reads
}

// TurnWindow reads the turn that opened at the newest owner line without a
// steer id. A steer line is an owner line too, so it joins the window, but it
// never moves the anchor. ok is false when the live tail holds no such line.
func TurnWindow(conv *aggregate.Conversation) (Window, bool) {
	all := conv.Messages()
	anchor := -1
	for i, m := range slices.Backward(all) {
		if m.Editable() && m.InputID() == "" {
			anchor = i
			break
		}
	}
	if anchor < 0 {
		return Window{}, false
	}
	w := Window{AnchorID: all[anchor].ID(), Owner: all[anchor].Content()}
	for _, m := range all[anchor+1:] {
		if m.Role() == enum.RoleSystem {
			continue
		}
		w.Msgs = append(w.Msgs, m)
		if m.Editable() {
			w.Owner = m.Content()
		}
	}
	w.Digest = digest(w.Msgs)
	return w, true
}

// modelCall reports whether the model issued a tool call. Injection calls are
// the application's own context records, not work the model did.
func modelCall(tc entity.ToolCall) bool { return tc.Type() != enum.TooltypeInjection }

// digest renders messages as lines: owner lines verbatim, other user text and
// model text clipped, each model call as its name and failed flag, and a failed
// call's output clipped.
func digest(msgs []entity.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		switch {
		case m.Editable():
			fmt.Fprintf(&b, "owner: %s\n", m.Content())
		case m.Role() == enum.RoleAssistant:
			fmt.Fprintf(&b, "assistant: %s\n", clip(m.Content(), digestClip))
		default:
			fmt.Fprintf(&b, "other: %s\n", clip(m.Content(), digestClip))
		}
		for _, tc := range m.ToolCalls() {
			if !modelCall(tc) {
				continue
			}
			fmt.Fprintf(&b, "tool %s failed=%t\n", tc.Name(), tc.Failed())
			if tc.Failed() {
				fmt.Fprintf(&b, "error: %s\n", clip(tc.Output(), digestClip))
			}
		}
	}
	return b.String()
}

// clip keeps the first n runes of s.
func clip(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}
