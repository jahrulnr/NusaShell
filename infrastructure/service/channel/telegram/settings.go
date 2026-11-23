package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Settings is the channel's stored document. The token is not in it.
type Settings struct {
	Enabled bool `json:"enabled"`
	// AllowedUserIDs are Telegram user ids; a lone "*" allows everyone.
	AllowedUserIDs []string `json:"allowedUserIds"`
	// AllowedGroupIDs are group chat ids (negative numbers).
	AllowedGroupIDs []string `json:"allowedGroupIds"`
	// GroupPolicy is when the bot answers in a group: disabled, mention
	// (an @mention, a reply to the bot, or a /command), or all.
	GroupPolicy string `json:"groupPolicy"`
}

// view is what Settings() returns: the document plus token state.
type view struct {
	Settings
	TokenSet    bool   `json:"tokenSet"`
	BotUsername string `json:"botUsername,omitempty"`
}

// update is what UpdateSettings accepts. Token: absent keeps it, "" removes
// it, a value replaces it. The add/remove lists change the allowlists
// without replacing them.
type update struct {
	Enabled         *bool     `json:"enabled"`
	AllowedUserIDs  *[]string `json:"allowedUserIds"`
	AllowedGroupIDs *[]string `json:"allowedGroupIds"`
	AddUserIDs      []string  `json:"addUserIds"`
	RemoveUserIDs   []string  `json:"removeUserIds"`
	AddGroupIDs     []string  `json:"addGroupIds"`
	RemoveGroupIDs  []string  `json:"removeGroupIds"`
	GroupPolicy     *string   `json:"groupPolicy"`
	Token           *string   `json:"token"`
}

func defaults() Settings {
	return Settings{Enabled: true, AllowedUserIDs: []string{}, AllowedGroupIDs: []string{}, GroupPolicy: "mention"}
}

// failClosed is used until settings load: off, and nobody allowed.
func failClosed() Settings {
	s := defaults()
	s.Enabled = false
	return s
}

var (
	userIDRe  = regexp.MustCompile(`^[0-9]+$`)
	groupIDRe = regexp.MustCompile(`^-[0-9]+$`)
	tokenRe   = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]{20,}$`)
)

// normalize trims the allowlists and drops repeats. An entry that is not
// a user id (or "*") or a group id is refused by name.
func (s *Settings) normalize() error {
	var err error
	if s.AllowedUserIDs, err = normalizeIDs(s.AllowedUserIDs, userIDRe, true, "a Telegram user id (digits)"); err != nil {
		return err
	}
	s.AllowedGroupIDs, err = normalizeIDs(s.AllowedGroupIDs, groupIDRe, false, "a Telegram group id (a negative number)")
	return err
}

func normalizeIDs(in []string, re *regexp.Regexp, wildcard bool, what string) ([]string, error) {
	out := []string{}
	for _, raw := range in {
		v := strings.TrimSpace(raw)
		if !(v == "*" && wildcard) && !re.MatchString(v) {
			return nil, fmt.Errorf("%q is not %s", raw, what)
		}
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out, nil
}

func (s Settings) validate() error {
	switch {
	case s.GroupPolicy != "disabled" && s.GroupPolicy != "mention" && s.GroupPolicy != "all":
		return fmt.Errorf("group policy %q: expected disabled, mention, or all", s.GroupPolicy)
	case slices.Contains(s.AllowedUserIDs, "*") && len(s.AllowedUserIDs)+len(s.AllowedGroupIDs) > 1:
		return errors.New("* must be the only allowlist entry")
	}
	return nil
}

// allows reports whether a sender in a chat passes the allowlist. Empty
// lists allow nobody: an unfinished setup stays silent instead of public.
// A group entry matches only that group chat.
func (s Settings) allows(senderID, chatID string) bool {
	for _, id := range s.AllowedUserIDs {
		if id == "*" || id == senderID {
			return true
		}
	}
	if strings.HasPrefix(chatID, "-") {
		return slices.Contains(s.AllowedGroupIDs, chatID)
	}
	return false
}

func isNotFound(err error) bool { return apperror.As(err).StatusCode() == apperror.StatusNotFound }

// load reads the stored document. A missing one is the defaults; an
// unreadable one keeps fallback rather than opening the channel.
func (c *Channel) load(ctx context.Context, fallback Settings) Settings {
	raw, err := c.settings.Get(ctx, name)
	if isNotFound(err) {
		return defaults()
	}
	if err != nil {
		c.log.Warn("could not read Telegram settings; keeping the previous ones", "error", err)
		return fallback
	}
	s := defaults()
	err = json.Unmarshal(raw, &s)
	if err == nil {
		err = s.normalize()
	}
	if err == nil {
		err = s.validate()
	}
	if err != nil {
		c.log.Warn("Telegram settings are corrupt; keeping the previous ones", "error", err)
		return fallback
	}
	return s
}

func (c *Channel) token(ctx context.Context) (string, error) {
	v, err := c.secrets.Get(ctx, drepo.ChannelSecretKey(name, "token"))
	if isNotFound(err) {
		return "", nil
	}
	return v, err
}

// Settings returns the document with token state, never the token.
func (c *Channel) Settings(ctx context.Context) (json.RawMessage, error) {
	token, err := c.token(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	v := view{Settings: c.cfg, TokenSet: token != ""}
	if c.me != nil {
		v.BotUsername = c.me.Username
	}
	c.mu.Unlock()
	return json.Marshal(v)
}

// UpdateSettings applies a partial change, saves it, and reconnects when
// the token or the enabled switch changed.
func (c *Channel) UpdateSettings(ctx context.Context, raw json.RawMessage) error {
	var u update
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&u); err != nil {
		return apperror.New(400, "invalid Telegram settings", err)
	}
	c.mu.Lock()
	next := c.cfg
	c.mu.Unlock()
	if u.Enabled != nil {
		next.Enabled = *u.Enabled
	}
	if u.AllowedUserIDs != nil {
		next.AllowedUserIDs = *u.AllowedUserIDs
	}
	if u.AllowedGroupIDs != nil {
		next.AllowedGroupIDs = *u.AllowedGroupIDs
	}
	if u.GroupPolicy != nil {
		next.GroupPolicy = *u.GroupPolicy
	}
	next.AllowedUserIDs = append(slices.Clone(next.AllowedUserIDs), u.AddUserIDs...)
	next.AllowedGroupIDs = append(slices.Clone(next.AllowedGroupIDs), u.AddGroupIDs...)
	if err := next.normalize(); err != nil {
		return apperror.New(400, err.Error(), nil)
	}
	next.AllowedUserIDs = slices.DeleteFunc(next.AllowedUserIDs, func(id string) bool {
		return slices.ContainsFunc(u.RemoveUserIDs, func(raw string) bool { return strings.TrimSpace(raw) == id })
	})
	next.AllowedGroupIDs = slices.DeleteFunc(next.AllowedGroupIDs, func(id string) bool {
		return slices.ContainsFunc(u.RemoveGroupIDs, func(raw string) bool { return strings.TrimSpace(raw) == id })
	})
	if err := next.validate(); err != nil {
		return apperror.New(400, err.Error(), nil)
	}
	tokenChanged := false
	if u.Token != nil {
		t := strings.TrimSpace(*u.Token)
		switch {
		case t == "":
			if err := c.secrets.Delete(ctx, drepo.ChannelSecretKey(name, "token")); err != nil && !isNotFound(err) {
				return err
			}
		case !tokenRe.MatchString(t):
			return apperror.New(400, "that does not look like a bot token from @BotFather (digits:letters)", nil)
		default:
			if err := c.secrets.Set(ctx, drepo.ChannelSecretKey(name, "token"), t); err != nil {
				return err
			}
		}
		tokenChanged = true
	}
	doc, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := c.settings.Put(ctx, name, doc); err != nil {
		return err
	}
	c.mu.Lock()
	wasEnabled := c.cfg.Enabled
	c.cfg = next
	c.mu.Unlock()
	switch {
	case !next.Enabled:
		_ = c.Stop()
		c.setState("disabled", "Enable Telegram to connect.")
	case tokenChanged || !wasEnabled:
		if t, _ := c.token(ctx); t == "" {
			_ = c.Stop()
			c.setState("unconfigured", "Add a bot token from @BotFather to connect.")
		} else {
			return c.Connect(ctx)
		}
	}
	return nil
}

const offsetKey = name + ".offset"

func (c *Channel) loadOffset(ctx context.Context) int64 {
	raw, err := c.settings.Get(ctx, offsetKey)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.Trim(string(raw), `"`), 10, 64)
	return n
}

func (c *Channel) saveOffset(offset int64) {
	_ = c.settings.Put(context.Background(), offsetKey, json.RawMessage(strconv.FormatInt(offset, 10)))
}
