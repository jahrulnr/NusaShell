package conversation

import (
	"mime"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
)

func (s *server) get(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	info, err := s.reads.Info(c.Request.Context(), id)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	out, err := s.project(c.Request.Context(), info)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, out)
}

// messages resolves ?start against the current history length —
// negative counts back from the end — then reads the absolute window
// through the broker. History may move between the two reads; the
// response carries the resolved indexes so the client stays honest
// rather than pretending a snapshot.
func (s *server) messages(c *gin.Context) {
	start, limit, ok := transport.ParseWindow(c, defaultWindowStart, defaultWindowLength, maxLimit)
	if !ok {
		return
	}
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	info, err := s.reads.Info(ctx, id)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	total := info.MessageCount
	effective := start
	if effective < 0 {
		effective += total
	}
	if effective < 0 {
		effective = 0
	}
	if effective > total {
		effective = total
	}
	end := min(effective+limit, total)
	msgs, err := s.reads.History(ctx, id, effective, end)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	out := messagePage{
		EffectiveStart: effective,
		Total:          total,
		HasMore:        effective+len(msgs) < total,
		Messages:       make([]messageDTO, 0, len(msgs)),
	}
	if out.HasMore {
		next := effective + len(msgs)
		out.NextStart = &next
	}
	for _, m := range msgs {
		out.Messages = append(out.Messages, toMessage(m))
	}
	c.JSON(200, out)
}

// export downloads a whole thread as one JSON document: its projection
// and every message in the shape the messages route serves.
func (s *server) export(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	info, err := s.reads.Info(ctx, id)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	msgs, err := s.reads.History(ctx, id, 0, info.MessageCount)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	// The thread's projection carries its waiting pick like every other
	// projection does, read now. An unreadable pick must not block the export of
	// the whole thread: the thread says so, and opening the room shows the reason.
	conv := toConversation(info)
	conv.Pending, conv.PendingUnreadable = pendingView(ctx, s.reads, id)
	s.applyLifecycle(ctx, info, &conv)
	out := struct {
		ExportedAt   time.Time       `json:"exportedAt"`
		Conversation conversationDTO `json:"conversation"`
		Messages     []messageDTO    `json:"messages"`
	}{ExportedAt: time.Now().UTC(), Conversation: conv, Messages: make([]messageDTO, 0, len(msgs))}
	for _, m := range msgs {
		out.Messages = append(out.Messages, toMessage(m))
	}
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "nusashell-" + info.ID + ".json"}))
	c.IndentedJSON(200, out)
}
