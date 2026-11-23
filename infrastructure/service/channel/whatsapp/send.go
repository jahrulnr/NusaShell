package whatsapp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/service/channel/markup"
)

// SendRequest is one message the owner sends on purpose (the CLI's
// `channel whatsapp send`), apart from any room. Type picks the shape:
//
//	text      text (Markdown, rendered as WhatsApp markup)
//	reply     text quoting messageId by senderId (quotedText optional)
//	edit      new text for messageId, a message this account sent
//	revoke    deletes messageId for everyone
//	reaction  text is the emoji on messageId by senderId ("" takes it back)
//	image, video, audio, document, file
//	          data (base64), mime, name, caption
//	sticker   data (base64 WebP), animated
//	location  latitude, longitude, name, address
//	contact   name, vcard
//	event     name, description, startTime, endTime (Unix seconds),
//	          joinLink, latitude/longitude
//	poll      text is the question; options (2-12), maxSelectable (0 = any)
type SendRequest struct {
	Target        string   `json:"target"`
	Type          string   `json:"type"`
	Text          string   `json:"text,omitempty"`
	MessageID     string   `json:"messageId,omitempty"`
	SenderID      string   `json:"senderId,omitempty"`
	QuotedText    string   `json:"quotedText,omitempty"`
	Data          string   `json:"data,omitempty"`
	MIME          string   `json:"mime,omitempty"`
	Caption       string   `json:"caption,omitempty"`
	Animated      bool     `json:"animated,omitempty"`
	Latitude      *float64 `json:"latitude,omitempty"`
	Longitude     *float64 `json:"longitude,omitempty"`
	Name          string   `json:"name,omitempty"`
	Address       string   `json:"address,omitempty"`
	VCard         string   `json:"vcard,omitempty"`
	Description   string   `json:"description,omitempty"`
	StartTime     int64    `json:"startTime,omitempty"`
	EndTime       int64    `json:"endTime,omitempty"`
	JoinLink      string   `json:"joinLink,omitempty"`
	Options       []string `json:"options,omitempty"`
	MaxSelectable int      `json:"maxSelectable,omitempty"`
}

// SendJSON sends one SendRequest and answers {"messageId": …}. It starts
// no turn; the message is paced and kept for search like any other.
func (c *Channel) SendJSON(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var req SendRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return nil, apperror.New(400, "invalid send request: "+err.Error(), err)
	}
	bad := func(msg string) error { return apperror.New(400, msg, nil) }
	switch req.Type {
	case "text", "reply", "edit", "revoke", "reaction", "image", "video", "audio", "document", "file",
		"sticker", "location", "contact", "event", "poll":
	default:
		return nil, bad(fmt.Sprintf("unknown type %q", req.Type))
	}
	target := strings.TrimSpace(req.Target)
	if !strings.Contains(target, "@") {
		target = normalizeNumber(target)
		if target == "" {
			return nil, bad("target must be a phone number or a JID")
		}
		target += "@" + types.DefaultUserServer
	}
	jid, err := types.ParseJID(target)
	if err != nil || jid.IsEmpty() {
		return nil, bad(fmt.Sprintf("invalid target %q", req.Target))
	}
	if !c.current().Enabled {
		return nil, bad("WhatsApp is turned off")
	}
	client := c.clientCopy()
	if client == nil || !client.IsLoggedIn() {
		return nil, bad("WhatsApp is not connected")
	}
	data := []byte(nil)
	if req.Data != "" {
		if data, err = base64.StdEncoding.DecodeString(strings.TrimSpace(req.Data)); err != nil {
			return nil, bad("data must be base64")
		}
		if len(data) > dservice.MaxChannelFile {
			return nil, bad(fmt.Sprintf("data is larger than %d MB", dservice.MaxChannelFile>>20))
		}
	}
	text := strings.TrimSpace(req.Text)
	reply := func(id string) (json.RawMessage, error) { return json.Marshal(map[string]string{"messageId": id}) }

	var msg *waE2E.Message
	switch req.Type {
	case "image", "video", "audio", "document", "file":
		if len(data) == 0 {
			return nil, bad(req.Type + " needs data (base64)")
		}
		mediaType := req.MIME
		if req.Type == "document" || req.Type == "file" {
			// A document stays a document whatever its bytes are.
			mediaType = "application/octet-stream"
			if req.MIME != "" && !strings.HasPrefix(req.MIME, "image/") && !strings.HasPrefix(req.MIME, "video/") && !strings.HasPrefix(req.MIME, "audio/") {
				mediaType = req.MIME
			}
		}
		id, err := c.sendFile(ctx, jid, dservice.ChannelFile{Name: req.Name, MediaType: mediaType, Data: data}, strings.TrimSpace(req.Caption), true)
		if err != nil {
			return nil, err
		}
		return reply(id)
	case "poll":
		if text == "" || len(req.Options) < 2 || len(req.Options) > 12 || req.MaxSelectable < 0 || req.MaxSelectable > len(req.Options) {
			return nil, bad("a poll needs a question (text), 2 to 12 options, and maxSelectable from 0 to the option count")
		}
		seen := map[string]bool{}
		for _, o := range req.Options {
			o = strings.TrimSpace(o)
			if o == "" || seen[o] {
				return nil, bad("poll options must be non-empty and distinct")
			}
			seen[o] = true
		}
		id, err := c.sendPoll(ctx, jid, text, req.Options, req.MaxSelectable, true)
		if err != nil {
			return nil, err
		}
		return reply(id)
	case "text":
		if text == "" {
			return nil, bad("text needs text")
		}
		body := markup.Render(text, markup.WhatsApp)
		msg = &waE2E.Message{Conversation: &body}
	case "reply":
		if text == "" || req.MessageID == "" || req.SenderID == "" {
			return nil, bad("reply needs text, messageId, and senderId")
		}
		body := markup.Render(text, markup.WhatsApp)
		msg = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: &body, ContextInfo: &waE2E.ContextInfo{
			StanzaID: new(req.MessageID), Participant: new(req.SenderID), QuotedMessage: &waE2E.Message{Conversation: new(req.QuotedText)}}}}
	case "edit":
		if text == "" || req.MessageID == "" {
			return nil, bad("edit needs messageId and text")
		}
		msg = client.BuildEdit(jid, types.MessageID(req.MessageID), &waE2E.Message{Conversation: new(markup.Render(text, markup.WhatsApp))})
	case "revoke":
		if req.MessageID == "" {
			return nil, bad("revoke needs messageId")
		}
		msg = client.BuildRevoke(jid, types.EmptyJID, types.MessageID(req.MessageID))
	case "reaction":
		if req.MessageID == "" || req.SenderID == "" {
			return nil, bad("reaction needs messageId and senderId")
		}
		sender, err := types.ParseJID(req.SenderID)
		if err != nil {
			return nil, bad("invalid senderId")
		}
		msg = client.BuildReaction(jid, sender, types.MessageID(req.MessageID), req.Text)
	case "sticker":
		if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
			return nil, bad("a sticker needs data: a base64 WebP image")
		}
		up, err := client.Upload(ctx, data, whatsmeow.MediaImage)
		if err != nil {
			return nil, fmt.Errorf("upload to WhatsApp: %w", err)
		}
		msg = &waE2E.Message{StickerMessage: &waE2E.StickerMessage{URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength,
			Mimetype: new("image/webp"), IsAnimated: new(req.Animated)}}
	case "location":
		loc, err := locationMessage(req)
		if err != nil {
			return nil, bad(err.Error())
		}
		msg = &waE2E.Message{LocationMessage: loc}
	case "contact":
		if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.VCard) == "" {
			return nil, bad("contact needs name and vcard")
		}
		msg = &waE2E.Message{ContactMessage: &waE2E.ContactMessage{DisplayName: new(req.Name), Vcard: new(req.VCard)}}
	case "event":
		if strings.TrimSpace(req.Name) == "" || req.StartTime <= 0 || (req.EndTime != 0 && req.EndTime < req.StartTime) {
			return nil, bad("event needs a name, startTime, and an endTime not before it")
		}
		ev := &waE2E.EventMessage{Name: new(req.Name), Description: new(req.Description), StartTime: new(req.StartTime), JoinLink: new(req.JoinLink)}
		if req.EndTime != 0 {
			ev.EndTime = new(req.EndTime)
		}
		if req.Latitude != nil || req.Longitude != nil {
			if ev.Location, err = locationMessage(req); err != nil {
				return nil, bad(err.Error())
			}
		}
		secret := make([]byte, 32)
		_, _ = rand.Read(secret)
		msg = &waE2E.Message{EventMessage: ev, MessageContextInfo: &waE2E.MessageContextInfo{MessageSecret: secret}}
	default:
		return nil, bad(fmt.Sprintf("unknown type %q", req.Type))
	}
	var id string
	err = c.paced(ctx, jid.String(), func(sendCtx context.Context) error {
		resp, err := client.SendMessage(sendCtx, jid, msg)
		if err != nil {
			return err
		}
		id = string(resp.ID)
		c.indexSent(sendCtx, jid, resp, msg, true)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reply(id)
}

func locationMessage(req SendRequest) (*waE2E.LocationMessage, error) {
	if req.Latitude == nil || req.Longitude == nil || *req.Latitude < -90 || *req.Latitude > 90 || *req.Longitude < -180 || *req.Longitude > 180 {
		return nil, errors.New("latitude and longitude are needed and must be valid coordinates")
	}
	return &waE2E.LocationMessage{DegreesLatitude: req.Latitude, DegreesLongitude: req.Longitude, Name: new(req.Name), Address: new(req.Address)}, nil
}
