package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Settings is the channel's stored document.
type Settings struct {
	Enabled bool `json:"enabled"`
	// AllowedNumbers are phone numbers (digits only once normalized); a
	// lone "*" allows everyone.
	AllowedNumbers []string `json:"allowedNumbers"`
	// AllowedGroups are group JIDs ("…@g.us") picked from the joined
	// groups list.
	AllowedGroups []string `json:"allowedGroups"`
	// GroupPolicy is when the device answers in a group: disabled, mention
	// (an @mention, a reply, or a /command), or all.
	GroupPolicy  string `json:"groupPolicy"`
	ReadReceipts bool   `json:"readReceipts"`
	SendTyping   bool   `json:"sendTyping"`
	// RetentionDays drops kept chat messages older than this many days
	// (0 keeps them).
	RetentionDays int `json:"retentionDays"`
	// SendPacing is the least gap in seconds between two messages to the
	// same chat (0 sends at once); QueueCap bounds the messages waiting
	// for one chat.
	SendPacing int `json:"sendPacing"`
	QueueCap   int `json:"queueCap"`
}

// update is what UpdateSettings accepts: absent fields keep their value.
// The add/remove lists change the allowlists without replacing them.
type update struct {
	Enabled        *bool     `json:"enabled"`
	AllowedNumbers *[]string `json:"allowedNumbers"`
	AllowedGroups  *[]string `json:"allowedGroups"`
	AddNumbers     []string  `json:"addNumbers"`
	RemoveNumbers  []string  `json:"removeNumbers"`
	AddGroups      []string  `json:"addGroups"`
	RemoveGroups   []string  `json:"removeGroups"`
	GroupPolicy    *string   `json:"groupPolicy"`
	ReadReceipts   *bool     `json:"readReceipts"`
	SendTyping     *bool     `json:"sendTyping"`
	RetentionDays  *int      `json:"retentionDays"`
	SendPacing     *int      `json:"sendPacing"`
	QueueCap       *int      `json:"queueCap"`
}

func defaults() Settings {
	return Settings{Enabled: true, AllowedNumbers: []string{}, AllowedGroups: []string{}, GroupPolicy: "mention",
		ReadReceipts: true, SendTyping: true, SendPacing: 5, QueueCap: 20}
}

// failClosed is used until settings load: off, and nobody allowed.
func failClosed() Settings {
	s := defaults()
	s.Enabled = false
	return s
}

// normalize canonicalizes the allowlists, dropping repeats. An entry that
// is neither a number (or "*") nor a group JID is refused by name.
func (s *Settings) normalize() error {
	nums := []string{}
	for _, raw := range s.AllowedNumbers {
		v := allowEntry(raw)
		if v == "" {
			return fmt.Errorf("%q is not a phone number; an empty allowlist allows no one, and * allows everyone", raw)
		}
		if !slices.Contains(nums, v) {
			nums = append(nums, v)
		}
	}
	s.AllowedNumbers = nums
	groups := []string{}
	for _, raw := range s.AllowedGroups {
		g := normalizeGroupJID(raw)
		if g == "" {
			return fmt.Errorf("%q is not a group JID (…@g.us); joined groups are listed under Groups", raw)
		}
		if !slices.Contains(groups, g) {
			groups = append(groups, g)
		}
	}
	s.AllowedGroups = groups
	return nil
}

func (s Settings) validate() error {
	switch {
	case s.GroupPolicy != "disabled" && s.GroupPolicy != "mention" && s.GroupPolicy != "all":
		return fmt.Errorf("group policy %q: expected disabled, mention, or all", s.GroupPolicy)
	case slices.Contains(s.AllowedNumbers, "*") && len(s.AllowedNumbers)+len(s.AllowedGroups) > 1:
		return errors.New("* must be the only allowlist entry")
	case s.RetentionDays < 0 || s.RetentionDays > 3650:
		return errors.New("retention must be 0 to 3650 days")
	case s.SendPacing < 0 || s.SendPacing > 60:
		return errors.New("send pacing must be 0 to 60 seconds")
	case s.QueueCap < 1 || s.QueueCap > 200:
		return errors.New("the send queue must hold 1 to 200 messages")
	}
	return nil
}

// allowEntry is the canonical form of one number allowlist entry: "*" or
// the digits; "" when it is neither.
func allowEntry(raw string) string {
	if strings.TrimSpace(raw) == "*" {
		return "*"
	}
	return normalizeNumber(raw)
}

// allows reports whether a sender in a chat passes the allowlist. Empty
// lists allow nobody. A group entry matches only that group chat.
func (s Settings) allows(senders, chats []string) bool {
	numbers := map[string]bool{}
	for _, n := range s.AllowedNumbers {
		if n == "*" {
			return true
		}
		numbers[n] = true
	}
	for _, c := range chats {
		if strings.HasSuffix(c, "@g.us") && slices.Contains(s.AllowedGroups, strings.ToLower(c)) {
			return true
		}
	}
	for _, j := range senders {
		if numbers[normalizeNumber(j)] {
			return true
		}
	}
	return false
}

// normalizeNumber keeps the digits of a phone number or JID user part.
func normalizeNumber(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	for _, suffix := range []string{"@s.whatsapp.net", "@lid", "@g.us"} {
		v = strings.TrimSuffix(v, suffix)
	}
	if i := strings.IndexByte(v, ':'); i >= 0 {
		v = v[:i]
	}
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, v)
}

// normalizeGroupJID canonicalizes "…@g.us"; anything else is dropped.
func normalizeGroupJID(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.IndexByte(v, ':'); i >= 0 {
		v = v[:i]
	}
	user, ok := strings.CutSuffix(v, "@g.us")
	if !ok || user == "" || strings.TrimLeft(user, "0123456789-") != "" {
		return ""
	}
	return user + "@g.us"
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
		c.log.Warn("could not read WhatsApp settings; keeping the previous ones", "error", err)
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
		c.log.Warn("WhatsApp settings are corrupt; keeping the previous ones", "error", err)
		return fallback
	}
	return s
}

// Settings returns the stored document.
func (c *Channel) Settings(context.Context) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return json.Marshal(c.cfg)
}

// UpdateSettings applies a partial change and saves it. Turning the channel
// off disconnects it; turning it on connects a paired device.
func (c *Channel) UpdateSettings(ctx context.Context, raw json.RawMessage) error {
	var u update
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&u); err != nil {
		return apperror.New(400, "invalid WhatsApp settings", err)
	}
	c.mu.Lock()
	next := c.cfg
	c.mu.Unlock()
	if u.Enabled != nil {
		next.Enabled = *u.Enabled
	}
	if u.AllowedNumbers != nil {
		next.AllowedNumbers = *u.AllowedNumbers
	}
	if u.AllowedGroups != nil {
		next.AllowedGroups = *u.AllowedGroups
	}
	next.AllowedNumbers = append(slices.Clone(next.AllowedNumbers), u.AddNumbers...)
	next.AllowedGroups = append(slices.Clone(next.AllowedGroups), u.AddGroups...)
	if u.GroupPolicy != nil {
		next.GroupPolicy = *u.GroupPolicy
	}
	if u.ReadReceipts != nil {
		next.ReadReceipts = *u.ReadReceipts
	}
	if u.SendTyping != nil {
		next.SendTyping = *u.SendTyping
	}
	if u.RetentionDays != nil {
		next.RetentionDays = *u.RetentionDays
	}
	if u.SendPacing != nil {
		next.SendPacing = *u.SendPacing
	}
	if u.QueueCap != nil {
		next.QueueCap = *u.QueueCap
	}
	if err := next.normalize(); err != nil {
		return apperror.New(400, err.Error(), nil)
	}
	// Removals compare canonical forms, so "+62 812…" drops "62812…".
	next.AllowedNumbers = slices.DeleteFunc(next.AllowedNumbers, func(n string) bool {
		return slices.ContainsFunc(u.RemoveNumbers, func(raw string) bool { return allowEntry(raw) == n })
	})
	next.AllowedGroups = slices.DeleteFunc(next.AllowedGroups, func(g string) bool {
		return slices.ContainsFunc(u.RemoveGroups, func(raw string) bool { return normalizeGroupJID(raw) == g })
	})
	if err := next.validate(); err != nil {
		return apperror.New(400, err.Error(), nil)
	}
	doc, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := c.settings.Put(ctx, name, doc); err != nil {
		return err
	}
	c.mu.Lock()
	was := c.cfg.Enabled
	c.cfg = next
	paired := c.client != nil && c.client.Store.ID != nil
	c.mu.Unlock()
	switch {
	case was && !next.Enabled:
		return c.Disconnect()
	case !was && next.Enabled && paired:
		return c.Connect(ctx)
	}
	return nil
}
