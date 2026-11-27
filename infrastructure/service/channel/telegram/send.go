package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// SendRequest is one message the owner sends on purpose — the CLI's
// `channel telegram send` — apart from any room. The Bot API lets a bot start
// a conversation with any chat that has already met it, so this is not a reply
// path: target is the chat id, thread the forum topic when there is one.
type SendRequest struct {
	Target string `json:"target"`
	Type   string `json:"type,omitempty"`
	Thread string `json:"thread,omitempty"`
	Text   string `json:"text,omitempty"`
}

// The send route reaches a channel through this interface; without it the
// route answers "this channel cannot send on request". Declared here so a
// signature drift fails the build instead of the owner's message.
var _ interface {
	SendJSON(context.Context, json.RawMessage) (json.RawMessage, error)
} = (*Channel)(nil)

// SendJSON sends one SendRequest and answers {"messageId": …}. Delivery is
// Channel.Send — the same markup, 4,096-unit split, retry-after, and unparsed
// fallback a reply uses — so a message sent on purpose reads like any other.
func (c *Channel) SendJSON(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var req SendRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return nil, apperror.New(400, "invalid send request: "+err.Error(), err)
	}
	bad := func(msg string) error { return apperror.New(400, msg, nil) }
	if req.Type != "" && req.Type != "text" {
		return nil, bad(fmt.Sprintf("unknown type %q: a send on request carries text today", req.Type))
	}
	// A private chat's id is the one Telegram gave the bot. It is numeric, and
	// saying so here keeps parseChat's failure out of the 502 class.
	target := strings.TrimSpace(req.Target)
	if _, err := strconv.ParseInt(target, 10, 64); err != nil {
		return nil, bad(fmt.Sprintf("target must be a numeric chat id, not %q", req.Target))
	}
	thread := strings.TrimSpace(req.Thread)
	if thread != "" {
		if _, err := strconv.ParseInt(thread, 10, 64); err != nil {
			return nil, bad(fmt.Sprintf("thread must be a numeric topic id, not %q", req.Thread))
		}
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return nil, bad("text needs text")
	}
	// A private chat has no topic: an empty thread is threadID 0, the Bot API's
	// own "this chat, not a forum topic".
	chat, err := valueobject.NewChatRef(name, target, thread)
	if err != nil {
		return nil, bad("invalid target: " + err.Error())
	}
	ids, err := c.Send(ctx, chat, text)
	if err != nil {
		return nil, sendFailure(err)
	}
	if len(ids) == 0 {
		return nil, apperror.New(502, "Telegram accepted the message but returned no id", nil)
	}
	return json.Marshal(map[string]any{"messageId": ids[0], "messageIds": ids})
}

// sendFailure puts a refusal in the owner's words. The 403 is the one a first
// message meets: no chat can be messaged until it has started the bot, and
// saying so is the difference between a mystery and an instruction.
func sendFailure(err error) error {
	var ae *apiError
	if !errors.As(err, &ae) || ae.code != 403 {
		return err
	}
	return apperror.New(403, "Telegram refused this chat ("+ae.desc+"): a chat can only be messaged after it has started the bot — the owner sends /start in the private chat, a group needs the bot added", err)
}
