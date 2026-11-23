// Package channel is the HTTP boundary for chat channels: statuses, each
// channel's own settings document, connect/disconnect/logout, the pairing
// QR, the chats linked to a room, and — for the CLI — WhatsApp's joined
// groups, explicit sends, user and contact lookups, and the search over
// the kept messages.
package channel

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	appchannel "github.com/jahrulnr/nusashell/application/channel"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Service is the channel use case. *appchannel.App satisfies it.
type Service interface {
	Statuses() []dservice.ChannelStatus
	Channel(name string) (dservice.Channel, bool)
	Links(ctx context.Context, convID string) ([]appchannel.LinkView, error)
	Chats(ctx context.Context) ([]appchannel.LinkView, error)
	Unlink(ctx context.Context, convID, key string) error
	Search(ctx context.Context, f drepo.ChatSearch) (drepo.ChatSearchPage, error)
}

// Register mounts the routes under /api. A nil service registers nothing.
func Register(api *gin.RouterGroup, svc Service) {
	if svc == nil {
		return
	}
	s := &server{svc: svc}
	api.GET("/channels", s.list)
	api.GET("/channels/:name/settings", s.settings)
	api.PUT("/channels/:name/settings", s.updateSettings)
	api.POST("/channels/:name/connect", s.action(func(ctx context.Context, ch dservice.Channel) error { return ch.Connect(ctx) }))
	api.POST("/channels/:name/disconnect", s.action(func(_ context.Context, ch dservice.Channel) error { return ch.Disconnect() }))
	api.POST("/channels/:name/logout", s.action(func(ctx context.Context, ch dservice.Channel) error { return ch.Logout(ctx) }))
	api.GET("/channels/:name/qr", s.qr)
	api.GET("/channels/:name/groups", s.groups)
	api.POST("/channels/:name/send", s.send)
	api.GET("/channels/:name/user", s.user)
	api.GET("/channels/:name/contacts", s.contacts)
	api.GET("/channels/:name/messages", s.messages)
	api.GET("/chats", s.chats)
	api.GET("/conversations/:id/links", s.links)
	api.DELETE("/conversations/:id/links", s.unlink)
}

type server struct{ svc Service }

func (s *server) channel(c *gin.Context) (dservice.Channel, bool) {
	ch, ok := s.svc.Channel(c.Param("name"))
	if !ok {
		transport.WriteProblem(c, 404, "not_found", "no such channel")
	}
	return ch, ok
}

func (s *server) list(c *gin.Context) {
	c.JSON(200, gin.H{"data": s.svc.Statuses()})
}

func (s *server) settings(c *gin.Context) {
	ch, ok := s.channel(c)
	if !ok {
		return
	}
	raw, err := ch.Settings(c.Request.Context())
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.Data(200, "application/json", raw)
}

func (s *server) updateSettings(c *gin.Context) {
	ch, ok := s.channel(c)
	if !ok {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil || !json.Valid(raw) {
		transport.WriteProblem(c, 400, "invalid_argument", "body must be a JSON object")
		return
	}
	if err := ch.UpdateSettings(c.Request.Context(), raw); err != nil {
		transport.RespondError(c, err)
		return
	}
	out, err := ch.Settings(c.Request.Context())
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.Data(200, "application/json", out)
}

func (s *server) action(do func(context.Context, dservice.Channel) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		ch, ok := s.channel(c)
		if !ok {
			return
		}
		if err := do(c.Request.Context(), ch); err != nil {
			transport.RespondError(c, apperror.New(400, err.Error(), err))
			return
		}
		c.JSON(200, ch.Status())
	}
}

func (s *server) qr(c *gin.Context) {
	ch, ok := s.channel(c)
	if !ok {
		return
	}
	png, err := ch.QR(c.Request.Context())
	if err != nil {
		transport.WriteProblem(c, 404, "not_found", err.Error())
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(200, "image/png", png)
}

func (s *server) groups(c *gin.Context) {
	ch, ok := s.channel(c)
	if !ok {
		return
	}
	if gl, ok := ch.(groupSource); ok {
		out, err := gl.GroupsJSON(c.Request.Context())
		if err != nil {
			transport.RespondError(c, apperror.New(400, err.Error(), err))
			return
		}
		c.Data(200, "application/json", out)
		return
	}
	transport.WriteProblem(c, 404, "not_found", "this channel has no groups to list")
}

// groupSource is implemented by channels that list joined groups.
type groupSource interface {
	GroupsJSON(ctx context.Context) (json.RawMessage, error)
}

// The optional capabilities a channel adapter may offer the CLI. Each
// takes and answers the adapter's own JSON shapes.
type (
	explicitSender interface {
		SendJSON(ctx context.Context, req json.RawMessage) (json.RawMessage, error)
	}
	userSource interface {
		UserJSON(ctx context.Context, id string) (json.RawMessage, error)
	}
	contactSource interface {
		ContactsJSON(ctx context.Context, query string, page, limit int) (json.RawMessage, error)
	}
)

// maxSendBody fits one base64 channel file plus the request around it.
const maxSendBody = dservice.MaxChannelFile/3*4 + 1<<20

// send delivers one message the owner sends on purpose, apart from any
// room. A failure past validation is the channel's (502).
func (s *server) send(c *gin.Context) {
	ch, ok := s.channel(c)
	if !ok {
		return
	}
	sender, ok := ch.(explicitSender)
	if !ok {
		transport.WriteProblem(c, 404, "not_found", "this channel cannot send on request")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, maxSendBody+1))
	if err != nil || len(raw) > maxSendBody || !json.Valid(raw) {
		transport.WriteProblem(c, 400, "invalid_argument", "body must be a JSON object within the file limit")
		return
	}
	out, err := sender.SendJSON(c.Request.Context(), raw)
	if err != nil {
		if apperror.As(err).StatusCode() < 500 {
			transport.RespondError(c, err)
		} else {
			transport.WriteProblem(c, 502, "send_failed", err.Error())
		}
		return
	}
	c.Data(200, "application/json", out)
}

func (s *server) user(c *gin.Context) {
	ch, ok := s.channel(c)
	if !ok {
		return
	}
	src, ok := ch.(userSource)
	if !ok {
		transport.WriteProblem(c, 404, "not_found", "this channel has no user lookup")
		return
	}
	out, err := src.UserJSON(c.Request.Context(), c.Query("id"))
	if err != nil {
		transport.RespondError(c, apperror.New(400, err.Error(), err))
		return
	}
	c.Data(200, "application/json", out)
}

func (s *server) contacts(c *gin.Context) {
	ch, ok := s.channel(c)
	if !ok {
		return
	}
	src, ok := ch.(contactSource)
	if !ok {
		transport.WriteProblem(c, 404, "not_found", "this channel has no contacts")
		return
	}
	page, err1 := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, err2 := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err1 != nil || err2 != nil {
		transport.WriteProblem(c, 400, "invalid_argument", "page and limit must be numbers")
		return
	}
	out, err := src.ContactsJSON(c.Request.Context(), c.Query("q"), page, limit)
	if err != nil {
		transport.RespondError(c, apperror.New(400, err.Error(), err))
		return
	}
	c.Data(200, "application/json", out)
}

// chatMessageDTO is one kept message; raw is the channel's own payload,
// present when asked for.
type chatMessageDTO struct {
	Chat       string          `json:"chat"`
	ChatName   string          `json:"chatName,omitempty"`
	ID         string          `json:"id"`
	Sender     string          `json:"sender"`
	SenderName string          `json:"senderName,omitempty"`
	At         time.Time       `json:"at"`
	FromMe     bool            `json:"fromMe"`
	Group      bool            `json:"group"`
	Text       string          `json:"text"`
	Receipt    string          `json:"receipt,omitempty"`
	Explicit   bool            `json:"explicit,omitempty"`
	Raw        json.RawMessage `json:"raw,omitempty"`
}

// messages searches one channel's kept messages, newest first.
func (s *server) messages(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil {
		transport.WriteProblem(c, 400, "invalid_argument", "limit must be a number")
		return
	}
	page, err := s.svc.Search(c.Request.Context(), drepo.ChatSearch{
		Channel: c.Param("name"), Query: c.Query("q"), Chat: c.Query("chat"), Sender: c.Query("sender"),
		Cursor: c.Query("cursor"), Limit: limit, Raw: c.Query("raw") == "1" || c.Query("raw") == "true",
	})
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	out := make([]chatMessageDTO, 0, len(page.Messages))
	for _, m := range page.Messages {
		out = append(out, chatMessageDTO{Chat: m.Chat, ChatName: m.ChatName, ID: m.ID, Sender: m.Sender, SenderName: m.SenderName,
			At: m.At, FromMe: m.FromMe, Group: m.Group, Text: m.Text, Receipt: string(m.Receipt), Explicit: m.Explicit, Raw: m.Raw})
	}
	c.JSON(200, gin.H{"data": out, "nextCursor": page.NextCursor})
}

// chats lists every linked chat — where an automation can send results.
func (s *server) chats(c *gin.Context) {
	out, err := s.svc.Chats(c.Request.Context())
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": out})
}

func (s *server) links(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	out, err := s.svc.Links(c.Request.Context(), id)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": out})
}

func (s *server) unlink(c *gin.Context) {
	id, ok := transport.PathID(c)
	if !ok {
		return
	}
	key, err := url.QueryUnescape(c.Query("chat"))
	if err != nil || key == "" {
		transport.WriteProblem(c, 400, "invalid_argument", "chat (the link key) is required")
		return
	}
	if err := s.svc.Unlink(c.Request.Context(), id, key); err != nil {
		transport.RespondError(c, err)
		return
	}
	c.Status(204)
}
