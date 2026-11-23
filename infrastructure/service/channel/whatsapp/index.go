package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/encoding/protojson"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

/* ---------------- the chat index ---------------- */

// indexMessage keeps one message of the account for search. It is apart
// from routing on purpose: keeping a message neither runs a room nor
// downloads media nor applies the allowlist. A revoke clears the original;
// an edit replaces the original's text.
func (c *Channel) indexMessage(ctx context.Context, evt *events.Message, chatName string, live, explicit bool) error {
	if c.index == nil || evt == nil || evt.Message == nil || evt.Info.Chat.IsEmpty() || evt.Info.ID == "" {
		return nil
	}
	info := evt.Info
	if info.Chat.Server == types.BroadcastServer || info.Chat.Server == types.NewsletterServer {
		return nil
	}
	chat := info.Chat.ToNonAD().String()
	msg, id := evt.Message, string(info.ID)
	if p := msg.GetProtocolMessage(); p != nil {
		switch p.GetType() {
		case waE2E.ProtocolMessage_REVOKE:
			if orig := p.GetKey().GetID(); orig != "" {
				return c.index.Revoke(ctx, name, chat, orig)
			}
			return nil
		case waE2E.ProtocolMessage_MESSAGE_EDIT:
			if p.GetEditedMessage() == nil || p.GetKey().GetID() == "" {
				return nil
			}
			msg, id = p.GetEditedMessage(), p.GetKey().GetID()
		default:
			return nil // key shares, history notices, and other plumbing
		}
	}
	text := indexText(msg)
	if text == "" {
		return nil // reactions, receipts of polls, and other content-free traffic
	}
	raw, err := protojson.Marshal(msg)
	if err != nil {
		return err
	}
	return c.index.Save(ctx, drepo.ChatMessage{
		Channel: name, Chat: chat, ID: id, Sender: info.Sender.ToNonAD().String(), SenderName: strings.TrimSpace(info.PushName),
		ChatName: chatName, At: info.Timestamp, FromMe: info.IsFromMe, Group: info.IsGroup,
		Text: text, Raw: raw, Live: live, Explicit: explicit,
	})
}

// indexText is a message's searchable text: its body or caption, a marker
// for media without one, and a poll's question and options.
func indexText(msg *waE2E.Message) string {
	text := waText(msg)
	if text == "" {
		switch {
		case msg.GetImageMessage() != nil:
			text = "[Image]"
		case msg.GetVideoMessage() != nil:
			text = "[Video]"
		case msg.GetAudioMessage() != nil:
			text = "[Audio]"
		case msg.GetStickerMessage() != nil:
			text = "[Sticker]"
		case msg.GetDocumentMessage() != nil:
			text = "[Document: " + msg.GetDocumentMessage().GetFileName() + "]"
		case msg.GetEventMessage() != nil:
			text = "[Event: " + msg.GetEventMessage().GetName() + "]"
		}
	}
	if p := pollCreation(msg); p != nil {
		text = joinText(text, pollText(p))
	}
	return strings.TrimSpace(text)
}

// indexSent keeps a message this channel sent. explicit marks a send the
// owner asked for (the CLI) rather than a room's reply.
func (c *Channel) indexSent(ctx context.Context, chat types.JID, resp whatsmeow.SendResponse, msg *waE2E.Message, explicit bool) {
	evt := &events.Message{Info: types.MessageInfo{
		MessageSource: types.MessageSource{Chat: chat, Sender: resp.Sender, IsFromMe: true, IsGroup: chat.Server == types.GroupServer},
		ID:            resp.ID, Timestamp: resp.Timestamp,
	}, Message: msg}
	chatName := ""
	if evt.Info.IsGroup {
		c.mu.Lock()
		chatName = c.groupNames[chat.ToNonAD().String()]
		c.mu.Unlock()
	}
	if err := c.indexMessage(ctx, evt, chatName, true, explicit); err != nil {
		c.log.Warn("could not keep a sent WhatsApp message for search", "error", err)
	}
}

// indexHistory keeps the messages of a history sync. A history copy never
// replaces one seen live.
func (c *Channel) indexHistory(evt *events.HistorySync) {
	client := c.clientCopy()
	if client == nil || evt == nil || evt.Data == nil || c.index == nil {
		return
	}
	c.mu.Lock()
	ctx := c.life
	c.mu.Unlock()
	for _, conv := range evt.Data.GetConversations() {
		chat, err := types.ParseJID(conv.GetID())
		if err != nil {
			continue
		}
		for _, item := range conv.GetMessages() {
			if item.GetMessage() == nil {
				continue
			}
			msg, err := client.ParseWebMessage(chat, item.GetMessage())
			if err == nil {
				err = c.indexMessage(ctx, msg, conv.GetDisplayName(), false, false)
			}
			if err != nil {
				c.log.Debug("could not keep a WhatsApp history message", "chat", chat.String(), "error", err)
			}
		}
	}
}

// retain drops kept messages past Settings.RetentionDays once a day, so a
// change takes effect without a restart.
func (c *Channel) retain(ctx context.Context) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		if days := c.current().RetentionDays; days > 0 && c.index != nil {
			n, err := c.index.Purge(ctx, name, time.Now().AddDate(0, 0, -days))
			switch {
			case err != nil:
				c.log.Warn("could not drop old WhatsApp messages", "error", err)
			case n > 0:
				c.log.Info("dropped WhatsApp messages past retention", "removed", n, "days", days)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// explicitPollOptions reads back the options of a poll the owner sent
// through a send request, from the kept copy, so its votes read as names
// after a restart. ok is false for any other poll.
func (c *Channel) explicitPollOptions(ctx context.Context, chat types.JID, id string) (names []string, ok bool) {
	if c.index == nil {
		return nil, false
	}
	m, err := c.index.Get(ctx, name, chat.ToNonAD().String(), id)
	if err != nil || !m.Explicit {
		return nil, false
	}
	var msg waE2E.Message
	if protojson.Unmarshal(m.Raw, &msg) != nil {
		return nil, true
	}
	for _, o := range pollCreation(&msg).GetOptions() {
		if n := strings.TrimSpace(o.GetOptionName()); n != "" {
			names = append(names, n)
		}
	}
	return names, true
}

/* ---------------- people ---------------- */

// UserJSON looks a WhatsApp user up through the running connection, so a
// lookup never links a second device.
func (c *Channel) UserJSON(ctx context.Context, target string) (json.RawMessage, error) {
	jid, err := types.ParseJID(strings.TrimSpace(target))
	if err != nil || jid.IsEmpty() || (jid.Server != types.DefaultUserServer && jid.Server != types.HiddenUserServer) {
		return nil, fmt.Errorf("%q is not a WhatsApp user JID (…@s.whatsapp.net or …@lid)", target)
	}
	client := c.clientCopy()
	if client == nil || !client.IsLoggedIn() {
		return nil, errors.New("WhatsApp is not connected")
	}
	infos, err := client.GetUserInfo(ctx, []types.JID{jid})
	if err != nil {
		return nil, err
	}
	contact, err := client.Store.Contacts.GetContact(ctx, jid)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"jid": jid.String(), "contact": contactView(jid, contact)}
	info, found := infos[jid]
	out["found"] = found
	if found {
		user := map[string]any{"status": info.Status, "pictureId": info.PictureID, "devices": len(info.Devices)}
		if !info.LID.IsEmpty() {
			user["lid"] = info.LID.String()
		}
		if info.VerifiedName != nil && info.VerifiedName.Details != nil {
			user["verifiedName"] = info.VerifiedName.Details.GetVerifiedName()
		}
		out["user"] = user
	}
	return json.Marshal(out)
}

func contactView(jid types.JID, info types.ContactInfo) map[string]any {
	return map[string]any{"jid": jid.String(), "firstName": info.FirstName, "fullName": info.FullName,
		"pushName": info.PushName, "businessName": info.BusinessName}
}

// ContactsJSON searches the contacts the linked device synced, by JID or
// any name, a page at a time. It reads the device store and needs no
// connection.
func (c *Channel) ContactsJSON(ctx context.Context, query string, page, limit int) (json.RawMessage, error) {
	if limit < 1 || limit > 500 || page < 1 {
		return nil, errors.New("limit must be 1 to 500 and page at least 1")
	}
	client := c.clientCopy()
	if client == nil || client.Store.ID == nil {
		return nil, errors.New("WhatsApp is not paired")
	}
	all, err := client.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	jids := make([]types.JID, 0, len(all))
	for jid, info := range all {
		hay := strings.ToLower(jid.String() + " " + info.FirstName + " " + info.FullName + " " + info.PushName + " " + info.BusinessName)
		if strings.Contains(hay, q) {
			jids = append(jids, jid)
		}
	}
	slices.SortFunc(jids, func(a, b types.JID) int { return strings.Compare(a.String(), b.String()) })
	start := min((page-1)*limit, len(jids))
	end := min(start+limit, len(jids))
	list := make([]map[string]any, 0, end-start)
	for _, jid := range jids[start:end] {
		list = append(list, contactView(jid, all[jid]))
	}
	return json.Marshal(map[string]any{"contacts": list, "page": page, "hasMore": end < len(jids)})
}
