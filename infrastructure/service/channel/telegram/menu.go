package telegram

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

var _ dservice.CommandMenu = (*Channel)(nil)

// commandNameRe is Telegram's command-name grammar; a name outside it
// cannot be published, so it is left out rather than repaired.
var commandNameRe = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// chatMenu is a chat's "/" menu: the channel's own commands, then the
// agent's whose names Telegram accepts and that do not clash. Telegram
// wants 3–256 character descriptions and at most 100 commands. nil means
// no agent command fits: the chat goes back to the default menu.
func chatMenu(cmds []dservice.AgentCommand) []botCommand {
	seen := make(map[string]bool, len(menuCommands))
	out := make([]botCommand, 0, len(menuCommands)+len(cmds))
	for _, bc := range menuCommands {
		seen[bc.Command] = true
		out = append(out, bc)
	}
	for _, c := range cmds {
		if len(out) >= 100 {
			break
		}
		if !commandNameRe.MatchString(c.Name) || seen[c.Name] {
			continue
		}
		desc := strings.TrimSpace(firstLine(c.Description))
		if desc == "" {
			desc = c.Name
		}
		if utf8.RuneCountInString(desc) < 3 {
			continue
		}
		if r := []rune(desc); len(r) > 256 {
			desc = string(r[:255]) + "…"
		}
		seen[c.Name] = true
		out = append(out, botCommand{Command: c.Name, Description: desc})
	}
	if len(out) == len(menuCommands) {
		return nil
	}
	return out
}

func firstLine(s string) string {
	if before, _, ok := strings.Cut(s, "\n"); ok {
		return before
	}
	return s
}

// SyncCommands publishes the chat's menu. It is quiet: a disconnected bot
// or a failed call is logged and skipped. The last menu each chat got is
// remembered, so a repeat costs no call.
func (c *Channel) SyncCommands(ctx context.Context, chat valueobject.ChatRef, cmds []dservice.AgentCommand) {
	client := c.clientCopy()
	chatID, _, err := parseChat(chat)
	if client == nil || err != nil || chatID == 0 {
		return
	}
	menu := chatMenu(cmds)
	fingerprint := "-" // the chat is on the default menu
	if menu != nil {
		raw, _ := json.Marshal(menu)
		fingerprint = string(raw)
	}
	key := strconv.FormatInt(chatID, 10)
	c.menuMu.Lock()
	defer c.menuMu.Unlock()
	if c.menus[key] == fingerprint {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, tokenTimeout)
	defer cancel()
	scope := map[string]any{"type": "chat", "chat_id": chatID}
	if menu == nil {
		err = client.deleteMyCommands(ctx, scope)
	} else {
		err = client.setMyCommands(ctx, menu, scope)
	}
	if err != nil {
		c.log.Warn("could not update a Telegram chat's command menu", "chat", key, "error", err)
		return
	}
	c.menus[key] = fingerprint
}
