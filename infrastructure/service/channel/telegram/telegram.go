// Package telegram is the Telegram channel: a Bot API client over plain
// HTTP that long-polls for messages, lets through the ones its allowlist
// and group policy accept, and sends replies rendered as Telegram HTML.
// The bot token lives in the secret store; everything else in the
// channel's settings document.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	"github.com/jahrulnr/nusashell/infrastructure/service/channel/markup"
)

const (
	name               = "telegram"
	apiBase            = "https://api.telegram.org"
	pollTimeout        = 50 * time.Second
	pollRequestTimeout = pollTimeout + 15*time.Second
	pollRetryDelay     = 3 * time.Second
	pollErrorLimit     = 20
	maxMessageAge      = 2 * time.Hour
	maxMessageUnits    = 4096
	typingEvery        = 4 * time.Second
	blockedLimit       = 12
	tokenTimeout       = 15 * time.Second
)

var menuCommands = []botCommand{
	{Command: "new", Description: "Start a new thread for this chat"},
	{Command: "use", Description: "List or switch models and agents"},
	{Command: "room", Description: "Show or switch this chat's thread"},
	{Command: "status", Description: "Show the thread and whether it is working"},
	{Command: "stop", Description: "Stop the running reply"},
	{Command: "approve", Description: "Approve the agent's request"},
	{Command: "deny", Description: "Deny the agent's request"},
	{Command: "help", Description: "List commands"},
}

// Channel is the Telegram adapter.
type Channel struct {
	settings drepo.ChannelSettings
	secrets  drepo.SecretStore
	log      *slog.Logger
	base     string

	mu         sync.Mutex
	handler    dservice.InboundHandler
	cfg        Settings
	client     *apiClient
	me         *tgUser
	state      string
	detail     string
	cancelPoll context.CancelFunc
	flow       uint64
	blocked    []dservice.BlockedSender
	updated    time.Time
	life       context.Context

	// albums buffers each media group until its items settle.
	albumMu sync.Mutex
	albums  map[string]*albumBatch

	// menus is what each chat's own "/" menu last became, by chat id; a
	// newly connected bot starts with none.
	menuMu sync.Mutex
	menus  map[string]string
}

// New builds the channel; nothing runs until Start.
func New(settings drepo.ChannelSettings, secrets drepo.SecretStore, log *slog.Logger) *Channel {
	if log == nil {
		log = slog.Default()
	}
	return &Channel{settings: settings, secrets: secrets, log: log.With("channel", name), base: apiBase,
		cfg: failClosed(), state: "disconnected", life: context.Background(), albums: map[string]*albumBatch{}, menus: map[string]string{}}
}

var _ dservice.Channel = (*Channel)(nil)

func (c *Channel) Name() string { return name }

// Start loads settings and connects when enabled with a token.
func (c *Channel) Start(ctx context.Context, handler dservice.InboundHandler) error {
	c.mu.Lock()
	c.handler = handler
	c.life = ctx
	c.cfg = c.load(ctx, c.cfg)
	enabled := c.cfg.Enabled
	c.mu.Unlock()
	token, _ := c.token(ctx)
	switch {
	case token == "":
		c.setState("unconfigured", "Add a bot token from @BotFather to connect.")
	case enabled:
		return c.Connect(ctx)
	default:
		c.setState("disabled", "Enable Telegram to connect.")
	}
	return nil
}

// Stop ends polling.
func (c *Channel) Stop() error {
	c.mu.Lock()
	if c.cancelPoll != nil {
		c.cancelPoll()
		c.cancelPoll = nil
	}
	c.client = nil
	c.mu.Unlock()
	c.flushAlbums()
	return nil
}

// Connect validates the token and starts the polling loop.
func (c *Channel) Connect(ctx context.Context) error {
	token, err := c.token(ctx)
	if err != nil || token == "" {
		return errors.New("add a bot token first")
	}
	c.mu.Lock()
	if !c.cfg.Enabled {
		c.mu.Unlock()
		return errors.New("enable Telegram first")
	}
	if c.cancelPoll != nil {
		c.cancelPoll()
	}
	c.flow++
	flow := c.flow
	pollCtx, cancel := context.WithCancel(c.life)
	c.cancelPoll = cancel
	c.state, c.detail = "connecting", "Checking the bot token…"
	c.updated = time.Now()
	c.mu.Unlock()
	go c.run(pollCtx, flow, newAPIClient(token, c.base))
	return nil
}

func (c *Channel) run(ctx context.Context, flow uint64, client *apiClient) {
	meCtx, cancel := context.WithTimeout(ctx, tokenTimeout)
	me, err := client.getMe(meCtx)
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			c.setFlowState(flow, "error", "The bot token was refused: "+err.Error())
		}
		return
	}
	c.mu.Lock()
	if c.flow != flow {
		c.mu.Unlock()
		return
	}
	c.client, c.me = client, me
	c.state, c.detail = "connected", ""
	c.updated = time.Now()
	c.mu.Unlock()
	c.log.Info("Telegram bot connected", "username", "@"+me.Username)
	// Chat menus published before belong to whichever bot was connected.
	c.menuMu.Lock()
	c.menus = map[string]string{}
	c.menuMu.Unlock()
	menuCtx, cancel := context.WithTimeout(ctx, tokenTimeout)
	if err := client.setMyCommands(menuCtx, menuCommands, map[string]any{"type": "default"}); err != nil {
		c.log.Warn("could not publish the Telegram command menu", "error", err)
	}
	cancel()
	c.poll(ctx, flow, client)
}

// poll is the getUpdates loop. The offset is stored after each batch, so a
// restart resumes after the last message taken; a redelivered message is
// dropped by the room side's dedupe.
func (c *Channel) poll(ctx context.Context, flow uint64, client *apiClient) {
	offset := c.loadOffset(ctx)
	failures := 0
	for ctx.Err() == nil {
		pctx, cancel := context.WithTimeout(ctx, pollRequestTimeout)
		updates, err := client.getUpdates(pctx, offset, int(pollTimeout/time.Second))
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if wait, limited := retryAfterDelay(err); limited {
				sleep(ctx, wait)
				continue
			}
			failures++
			c.log.Warn("Telegram getUpdates failed", "error", err, "consecutive", failures)
			if failures >= pollErrorLimit {
				c.setFlowState(flow, "error", fmt.Sprintf("Polling stopped after %d failures: %v", failures, err))
				return
			}
			sleep(ctx, pollRetryDelay)
			continue
		}
		failures = 0
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			if u.Message != nil {
				c.handle(ctx, u.Message)
			}
			if u.InlineQuery != nil {
				c.handleInlineQuery(ctx, u.InlineQuery)
			}
		}
		if len(updates) > 0 {
			c.saveOffset(offset)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// handle gates one message and hands it on.
func (c *Channel) handle(ctx context.Context, m *tgMessage) {
	c.mu.Lock()
	cfg, me, handler := c.cfg, c.me, c.handler
	c.mu.Unlock()
	if !cfg.Enabled || handler == nil || m.From == nil || m.From.ID == 0 || m.From.IsBot {
		return
	}
	group := m.Chat.Type == "group" || m.Chat.Type == "supergroup"
	if !group && m.Chat.Type != "private" {
		return
	}
	text := strings.TrimSpace(m.Text)
	if text == "" {
		text = strings.TrimSpace(m.Caption)
	}
	if group {
		switch cfg.GroupPolicy {
		case "disabled":
			return
		case "mention":
			if !mentionsMe(m, me) && !strings.HasPrefix(text, "/") {
				return
			}
		}
	}
	if m.Date > 0 && time.Since(time.Unix(m.Date, 0)) > maxMessageAge {
		return
	}
	chatID := strconv.FormatInt(m.Chat.ID, 10)
	senderID := strconv.FormatInt(m.From.ID, 10)
	sender := displayName(m.From)
	if !cfg.allows(senderID, chatID) {
		c.recordBlocked(senderID, chatID, sender, group)
		return
	}
	if me != nil && me.Username != "" {
		text = strings.TrimSpace(stripMention(text, "@"+me.Username))
	}
	thread := ""
	if m.MessageThreadID != 0 {
		thread = strconv.FormatInt(m.MessageThreadID, 10)
	}
	chat, err := valueobject.NewChatRef(name, chatID, thread)
	if err != nil {
		return
	}
	chatName := m.Chat.Title
	if !group {
		chatName = sender
	}
	// Media is downloaded before the message goes on; a file that does not
	// arrive leaves a bracketed marker so the room knows it was lost.
	var files []dservice.ChannelFile
	if cands := mediaCandidates(m); len(cands) > 0 {
		client := c.clientCopy()
		for _, cand := range cands {
			if client == nil {
				text = joinText(text, "["+cand.kind+" could not be downloaded]")
				continue
			}
			f, marker := c.download(ctx, client, m, cand)
			if marker != "" {
				text = joinText(text, marker)
				continue
			}
			files = append(files, f)
		}
	}
	in := dservice.InboundMessage{
		Chat: chat, MessageID: chatID + ":" + strconv.FormatInt(m.MessageID, 10),
		SenderID: senderID, SenderName: sender, ChatName: chatName,
		Text: text, Quote: quoteOf(m.ReplyToMessage), Group: group, Files: files, At: time.Unix(m.Date, 0),
		// A pick from this bot's inline mode: its @tokens name rooms and
		// projects.
		Inline: m.ViaBot != nil && me != nil && me.Username != "" && strings.EqualFold(m.ViaBot.Username, me.Username),
	}
	if m.MediaGroupID != "" {
		c.bufferAlbum(m, in)
		return
	}
	handler.HandleInbound(ctx, in)
}

// quoteOf is what a reply quotes: the replied-to message's text or caption,
// or a label for what it was when it had neither ("an image"). It is empty
// for a message that replies to nothing.
func quoteOf(m *tgMessage) string {
	if m == nil {
		return ""
	}
	if text := strings.TrimSpace(m.Text); text != "" {
		return text
	}
	if text := strings.TrimSpace(m.Caption); text != "" {
		return text
	}
	switch {
	case len(m.Photo) > 0:
		return "an image"
	case m.Video != nil, m.Animation != nil:
		return "a video"
	case m.Voice != nil:
		return "a voice message"
	case m.Audio != nil:
		return "an audio file"
	case m.Sticker != nil:
		return "a sticker"
	case m.Document != nil:
		return "a document"
	case m.VideoNote != nil:
		return "a video note"
	}
	return "a message"
}

// mentionsMe reports a message that addresses the bot: an @mention, a
// text_mention of it, a reply to one of its messages, or one written
// through its inline mode.
func mentionsMe(m *tgMessage, me *tgUser) bool {
	if me == nil {
		return false
	}
	if m.ViaBot != nil && m.ViaBot.ID == me.ID {
		return true // written through this bot's inline mode
	}
	if me.Username != "" {
		needle := "@" + strings.ToLower(me.Username)
		if containsMentionToken(m.Text, needle) || containsMentionToken(m.Caption, needle) {
			return true
		}
	}
	for _, group := range [][]tgEntity{m.Entities, m.CaptionEntities} {
		for _, e := range group {
			if e.Type == "text_mention" && e.User != nil && e.User.ID == me.ID {
				return true
			}
		}
	}
	return m.ReplyToMessage != nil && m.ReplyToMessage.From != nil && m.ReplyToMessage.From.ID == me.ID
}

// stripMention removes a whole-token @mention of the bot so the room reads
// the request, not the address.
func stripMention(text, mention string) string {
	lower := strings.ToLower(text)
	needle := strings.ToLower(mention)
	if i := strings.Index(lower, needle); i >= 0 && containsMentionToken(text[i:i+len(needle)], needle) {
		return text[:i] + text[i+len(needle):]
	}
	return text
}

// containsMentionToken finds needle as a whole @username token.
func containsMentionToken(text, needle string) bool {
	needle = strings.ToLower(needle)
	lower := strings.ToLower(text)
	for i := 0; ; {
		j := strings.Index(lower[i:], needle)
		if j < 0 {
			return false
		}
		at := i + j
		before := at == 0
		if !before {
			r, _ := utf8.DecodeLastRuneInString(text[:at])
			before = !isUsernameRune(r)
		}
		end := at + len(needle)
		after := end >= len(text)
		if !after {
			r, _ := utf8.DecodeRuneInString(text[end:])
			after = !isUsernameRune(r)
		}
		if before && after {
			return true
		}
		i = at + 1
	}
}

func isUsernameRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func displayName(u *tgUser) string {
	n := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if n == "" && u.Username != "" {
		n = "@" + u.Username
	}
	if n == "" {
		n = strconv.FormatInt(u.ID, 10)
	}
	return n
}

func (c *Channel) recordBlocked(senderID, chatID, sender string, group bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.blocked[:0]
	for _, b := range c.blocked {
		if b.SenderID != senderID || b.ChatID != chatID {
			out = append(out, b)
		}
	}
	out = append(out, dservice.BlockedSender{SenderID: senderID, ChatID: chatID, Name: sender, Group: group, At: time.Now()})
	if len(out) > blockedLimit {
		out = out[len(out)-blockedLimit:]
	}
	c.blocked = out
	c.updated = time.Now()
	c.log.Info("ignored a Telegram message from a sender not on the allowlist", "sender", senderID, "chat", chatID)
}

func parseChat(chat valueobject.ChatRef) (chatID, threadID int64, err error) {
	chatID, err = strconv.ParseInt(chat.ChatID(), 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid Telegram chat id %q", chat.ChatID())
	}
	if chat.Thread() != "" {
		threadID, err = strconv.ParseInt(chat.Thread(), 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid Telegram thread id %q", chat.Thread())
		}
	}
	return chatID, threadID, nil
}

func (c *Channel) clientCopy() *apiClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client
}

// Send renders Markdown as Telegram HTML and sends it; a reply over the
// 4,096-unit cap is split. If Telegram rejects the markup, the text goes
// out unformatted rather than not at all.
func (c *Channel) Send(ctx context.Context, chat valueobject.ChatRef, md string) ([]string, error) {
	chatID, threadID, err := parseChat(chat)
	if err != nil {
		return nil, err
	}
	client := c.clientCopy()
	if client == nil {
		return nil, errors.New("Telegram is not connected")
	}
	var ids []string
	for _, part := range splitText(strings.TrimSpace(md), maxMessageUnits-200) {
		sent, err := client.sendMessage(ctx, chatID, threadID, markup.Render(part, markup.TelegramHTML), "HTML")
		if wait, limited := retryAfterDelay(err); limited {
			sleep(ctx, wait)
			sent, err = client.sendMessage(ctx, chatID, threadID, markup.Render(part, markup.TelegramHTML), "HTML")
		}
		if err != nil && isParseError(err) {
			sent, err = client.sendMessage(ctx, chatID, threadID, truncateUTF16(part, maxMessageUnits), "")
		}
		if err != nil {
			return ids, err
		}
		if sent != nil {
			ids = append(ids, strconv.FormatInt(sent.MessageID, 10))
		}
	}
	return ids, nil
}

// splitText cuts text into pieces of at most limit UTF-16 units, at line
// breaks where it can.
func splitText(text string, limit int) []string {
	var out []string
	for utf16Units(text) > limit {
		cut := truncateUTF16(text, limit)
		if i := strings.LastIndex(cut, "\n"); i > len(cut)/2 {
			cut = cut[:i]
		}
		out = append(out, cut)
		text = strings.TrimLeft(text[len(cut):], "\n")
	}
	if text != "" || len(out) == 0 {
		out = append(out, text)
	}
	return out
}

// Typing shows "typing…" until stop is called.
func (c *Channel) Typing(ctx context.Context, chat valueobject.ChatRef) func() {
	chatID, threadID, err := parseChat(chat)
	client := c.clientCopy()
	if err != nil || client == nil {
		return func() {}
	}
	tctx, cancel := context.WithCancel(ctx)
	go func() {
		for {
			_ = client.sendChatAction(tctx, chatID, threadID)
			select {
			case <-tctx.Done():
				return
			case <-time.After(typingEvery):
			}
		}
	}()
	return cancel
}

// Disconnect stops polling until Connect.
func (c *Channel) Disconnect() error {
	_ = c.Stop()
	c.setState("disconnected", "Disconnected. Connect to resume.")
	return nil
}

// Logout forgets the bot token.
func (c *Channel) Logout(ctx context.Context) error {
	_ = c.Stop()
	if err := c.secrets.Delete(ctx, drepo.ChannelSecretKey(name, "token")); err != nil && !isNotFound(err) {
		return err
	}
	c.mu.Lock()
	c.me = nil
	c.mu.Unlock()
	c.setState("unconfigured", "Bot token removed. Add one to connect again.")
	return nil
}

// QR is not how Telegram pairs.
func (c *Channel) QR(context.Context) ([]byte, error) {
	return nil, errors.New("Telegram connects with a bot token, not a QR code")
}

func (c *Channel) setState(state, detail string) {
	c.mu.Lock()
	c.state, c.detail = state, detail
	c.updated = time.Now()
	c.mu.Unlock()
}

func (c *Channel) setFlowState(flow uint64, state, detail string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.flow != flow {
		return
	}
	c.state, c.detail = state, detail
	c.client = nil
	c.updated = time.Now()
}

// Status reports the channel.
func (c *Channel) Status() dservice.ChannelStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := dservice.ChannelStatus{Name: name, Label: "Telegram", Enabled: c.cfg.Enabled, State: c.state, Detail: c.detail,
		Connected: c.state == "connected", Blocked: append([]dservice.BlockedSender{}, c.blocked...), UpdatedAt: c.updated}
	if c.me != nil {
		st.Account = "@" + c.me.Username
	}
	return st
}
