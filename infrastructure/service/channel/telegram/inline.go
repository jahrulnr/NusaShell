package telegram

import (
	"context"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// inlineTimeout bounds one inline answer: the user is typing.
const inlineTimeout = 8 * time.Second

// handleInlineQuery answers one inline query with the rooms and projects
// the app suggests. The first word filters; tapping a result sends
// "@handle" followed by the rest of the query as typed. A sender the
// allowlist turns away gets an empty answer, so their client stops
// waiting.
func (c *Channel) handleInlineQuery(ctx context.Context, q *tgInlineQuery) {
	c.mu.Lock()
	cfg, handler := c.cfg, c.handler
	c.mu.Unlock()
	client := c.clientCopy()
	if q.From == nil || client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, inlineTimeout)
	defer cancel()
	var results []inlineResult
	if cfg.Enabled && handler != nil && cfg.allows(strconv.FormatInt(q.From.ID, 10), "") {
		token, rest := splitInlineQuery(q.Query)
		for _, m := range handler.Mentions(ctx, token) {
			results = append(results, inlineResult{Type: "article", ID: m.Kind + ":" + m.ID, Title: m.Token,
				Description: m.Title, InputMessageContent: map[string]any{"message_text": m.Token + rest}})
		}
	}
	if err := client.answerInlineQuery(ctx, q.ID, results); err != nil {
		c.log.Warn("could not answer a Telegram inline query", "error", err)
	}
}

// splitInlineQuery separates the first word (the filter) from the rest,
// which keeps its spacing: "cat  look" → "cat", "  look".
func splitInlineQuery(query string) (token, rest string) {
	q := strings.TrimLeft(query, " ")
	if i := strings.IndexFunc(q, unicode.IsSpace); i >= 0 {
		return q[:i], q[i:]
	}
	return q, ""
}
