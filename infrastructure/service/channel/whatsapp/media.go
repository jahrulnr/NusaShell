package whatsapp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// pollCacheLimit bounds the option names kept to read votes back.
const pollCacheLimit = 512

var (
	_ dservice.FileSender = (*Channel)(nil)
	_ dservice.PollSender = (*Channel)(nil)
)

/* ---------------- inbound media ---------------- */

// inboundFiles downloads the media a message carries. Each file that does
// not arrive adds a bracketed marker to text, so the room knows it was lost.
func (c *Channel) inboundFiles(ctx context.Context, msg *waE2E.Message, id types.MessageID, text string) ([]dservice.ChannelFile, string) {
	var files []dservice.ChannelFile
	take := func(kind, stem, fallbackExt, fileName, mediaType string, m whatsmeow.DownloadableMessage, size uint64) {
		data, err := c.download(ctx, m, size)
		if err != nil {
			c.log.Warn("could not download a WhatsApp file", "kind", kind, "error", err)
			text = joinText(text, "["+kind+" could not be downloaded]")
			return
		}
		mediaType = strings.TrimSpace(strings.Split(mediaType, ";")[0])
		if mediaType == "" {
			mediaType = http.DetectContentType(data)
		}
		fileName = filepath.Base(strings.TrimSpace(fileName))
		if fileName == "" || fileName == "." || fileName == "/" {
			fileName = stem + "-" + id + extensionFor(mediaType, fallbackExt)
		}
		files = append(files, dservice.ChannelFile{Name: fileName, MediaType: mediaType, Data: data})
	}
	if x := msg.GetImageMessage(); x != nil {
		take("Image", "image", ".jpg", "", x.GetMimetype(), x, x.GetFileLength())
	}
	if x := msg.GetDocumentMessage(); x != nil {
		take("Document", "document", "", x.GetFileName(), x.GetMimetype(), x, x.GetFileLength())
	}
	// A video note (the round bubble) shares the video payload.
	if x := msg.GetVideoMessage(); x != nil {
		take("Video", "video", ".mp4", "", x.GetMimetype(), x, x.GetFileLength())
	}
	if x := msg.GetPtvMessage(); x != nil {
		take("Video note", "video-note", ".mp4", "", x.GetMimetype(), x, x.GetFileLength())
	}
	if x := msg.GetAudioMessage(); x != nil {
		kind := "Audio"
		if x.GetPTT() {
			kind = "Voice note"
		}
		take(kind, "audio", ".ogg", "", x.GetMimetype(), x, x.GetFileLength())
	}
	if x := msg.GetStickerMessage(); x != nil {
		take("Sticker", "sticker", ".webp", "", x.GetMimetype(), x, x.GetFileLength())
	}
	return files, text
}

func (c *Channel) download(ctx context.Context, m whatsmeow.DownloadableMessage, size uint64) ([]byte, error) {
	if size > dservice.MaxChannelFile {
		return nil, fmt.Errorf("the file is larger than %d MB", dservice.MaxChannelFile>>20)
	}
	client := c.clientCopy()
	if client == nil {
		return nil, errors.New("WhatsApp is not connected")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	data, err := client.Download(ctx, m)
	if err != nil {
		return nil, err
	}
	if len(data) > dservice.MaxChannelFile {
		return nil, fmt.Errorf("the file is larger than %d MB", dservice.MaxChannelFile>>20)
	}
	return data, nil
}

func extensionFor(mediaType, fallback string) string {
	switch strings.ToLower(mediaType) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	case "video/3gpp":
		return ".3gp"
	case "audio/ogg":
		return ".ogg"
	case "audio/opus":
		return ".opus"
	case "audio/mpeg":
		return ".mp3"
	case "audio/mp4":
		return ".m4a"
	case "audio/aac":
		return ".aac"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "application/pdf":
		return ".pdf"
	}
	return fallback
}

// joinText appends one more block without repeating identical text.
func joinText(text, extra string) string {
	text = strings.TrimSpace(text)
	extra = strings.TrimSpace(extra)
	if extra == "" {
		return text
	}
	if text == "" || text == extra {
		return extra
	}
	return text + "\n\n" + extra
}

/* ---------------- outbound media ---------------- */

// SendFile uploads one file and sends it: images as photos, video as
// video, OGG/Opus audio as a voice note, other audio as audio, and
// everything else as a document with its name.
func (c *Channel) SendFile(ctx context.Context, chat valueobject.ChatRef, f dservice.ChannelFile) error {
	jid, err := parseJID(chat)
	if err != nil {
		return err
	}
	_, err = c.sendFile(ctx, jid, f, "", false)
	return err
}

// sendFile uploads and sends one file, paced, and keeps it for search.
// Rooms' reply files and the owner's explicit sends share it.
func (c *Channel) sendFile(ctx context.Context, jid types.JID, f dservice.ChannelFile, caption string, explicit bool) (string, error) {
	if len(f.Data) == 0 {
		return "", errors.New("the file is empty")
	}
	if len(f.Data) > dservice.MaxChannelFile {
		return "", fmt.Errorf("the file is larger than %d MB", dservice.MaxChannelFile>>20)
	}
	client := c.clientCopy()
	if client == nil || !client.IsLoggedIn() {
		return "", errors.New("WhatsApp is not connected")
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(f.MediaType, ";")[0]))
	if mediaType == "" {
		mediaType = http.DetectContentType(f.Data)
	}
	kind := whatsmeow.MediaDocument
	switch {
	case strings.HasPrefix(mediaType, "image/"):
		kind = whatsmeow.MediaImage
	case strings.HasPrefix(mediaType, "video/"):
		kind = whatsmeow.MediaVideo
	case strings.HasPrefix(mediaType, "audio/"):
		kind = whatsmeow.MediaAudio
	}
	var id string
	err := c.paced(ctx, jid.String(), func(sendCtx context.Context) error {
		up, err := client.Upload(sendCtx, f.Data, kind)
		if err != nil {
			return fmt.Errorf("upload to WhatsApp: %w", err)
		}
		msg := fileMessage(up, kind, mediaType, f.Name, caption)
		resp, err := client.SendMessage(sendCtx, jid, msg)
		if err != nil {
			return err
		}
		id = resp.ID
		c.indexSent(sendCtx, jid, resp, msg, explicit)
		return nil
	})
	return id, err
}

// fileMessage builds the message for an uploaded file. Audio carries no
// caption on WhatsApp.
func fileMessage(up whatsmeow.UploadResponse, kind whatsmeow.MediaType, mediaType, fileName, caption string) *waE2E.Message {
	var capt *string
	if caption != "" {
		capt = &caption
	}
	switch kind {
	case whatsmeow.MediaImage:
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{URL: &up.URL, DirectPath: &up.DirectPath,
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
			FileLength: &up.FileLength, Mimetype: new(mediaType), Caption: capt}}
	case whatsmeow.MediaVideo:
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{URL: &up.URL, DirectPath: &up.DirectPath,
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
			FileLength: &up.FileLength, Mimetype: new(mediaType), StreamingSidecar: up.StreamingSidecar, Caption: capt}}
	case whatsmeow.MediaAudio:
		m := &waE2E.AudioMessage{URL: &up.URL, DirectPath: &up.DirectPath,
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
			FileLength: &up.FileLength, Mimetype: new(mediaType)}
		// OGG/Opus clips show as voice-note bubbles.
		if mediaType == "audio/ogg" || mediaType == "audio/opus" {
			m.PTT = new(true)
		}
		return &waE2E.Message{AudioMessage: m}
	}
	fileName = filepath.Base(strings.TrimSpace(fileName))
	if fileName == "" || fileName == "." || fileName == "/" {
		fileName = "file"
	}
	return &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{URL: &up.URL, DirectPath: &up.DirectPath,
		MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
		FileLength: &up.FileLength, Mimetype: new(mediaType), FileName: new(fileName), Caption: capt}}
}

/* ---------------- polls ---------------- */

// SendPoll sends a native poll and remembers its options, so votes on it
// read back as option names. The poll's message id comes back.
func (c *Channel) SendPoll(ctx context.Context, chat valueobject.ChatRef, question string, options []string, maxSelectable int) (string, error) {
	jid, err := parseJID(chat)
	if err != nil {
		return "", err
	}
	return c.sendPoll(ctx, jid, question, options, maxSelectable, false)
}

// sendPoll sends a poll, paced, keeps it for search, and remembers its
// options. A room's polls and the owner's explicit ones share it; votes
// on an explicit poll reach the room as text (see handleMessage).
func (c *Channel) sendPoll(ctx context.Context, jid types.JID, question string, options []string, maxSelectable int, explicit bool) (string, error) {
	// Trim once so the wire options match the names kept for vote hashes.
	names := make([]string, 0, len(options))
	for _, o := range options {
		if o = strings.TrimSpace(o); o != "" {
			names = append(names, o)
		}
	}
	if len(names) < 2 {
		return "", errors.New("a poll needs at least two options")
	}
	client := c.clientCopy()
	if client == nil || !client.IsLoggedIn() {
		return "", errors.New("WhatsApp is not connected")
	}
	msg := client.BuildPollCreation(question, names, maxSelectable)
	var id string
	err := c.paced(ctx, jid.String(), func(sendCtx context.Context) error {
		resp, err := client.SendMessage(sendCtx, jid, msg)
		if err != nil {
			return err
		}
		id = resp.ID
		c.rememberPoll(jid.String(), id, names)
		c.indexSent(sendCtx, jid, resp, msg, explicit)
		return nil
	})
	return id, err
}

// pollCreation finds a poll in any of the message's poll shapes.
func pollCreation(msg *waE2E.Message) *waE2E.PollCreationMessage {
	if msg == nil {
		return nil
	}
	for _, p := range []*waE2E.PollCreationMessage{
		msg.GetPollCreationMessage(), msg.GetPollCreationMessageV2(), msg.GetPollCreationMessageV3(),
		msg.GetPollCreationMessageV5(), msg.GetPollCreationMessageV6(),
	} {
		if p != nil {
			return p
		}
	}
	for _, fp := range []*waE2E.FutureProofMessage{msg.GetPollCreationMessageV4(), msg.GetPollCreationOptionImageMessage()} {
		if p := pollCreation(fp.GetMessage()); p != nil {
			return p
		}
	}
	return nil
}

// isPollTraffic is a poll, a vote, or an added option: none of them can
// carry an @mention, so a mention-only group still lets them through.
func isPollTraffic(msg *waE2E.Message) bool {
	return pollCreation(msg) != nil || msg.GetPollUpdateMessage() != nil || msg.GetPollAddOptionMessage() != nil
}

func pollText(p *waE2E.PollCreationMessage) string {
	var b strings.Builder
	b.WriteString("[Poll] " + p.GetName())
	for _, o := range p.GetOptions() {
		if name := strings.TrimSpace(o.GetOptionName()); name != "" {
			b.WriteString("\n- " + name)
		}
	}
	switch n := p.GetSelectableOptionsCount(); {
	case n == 0:
		b.WriteString("\n(multiple answers allowed)")
	case n > 1:
		fmt.Fprintf(&b, "\n(choose up to %d)", n)
	}
	return b.String()
}

// pollKeys are the cache keys of one poll: the chat JID as given and its
// device-free form, so a vote whose embedded JID differs only by device
// still resolves.
func pollKeys(chatJID, msgID string) []string {
	keys := []string{chatJID + ":" + msgID}
	if jid, err := types.ParseJID(chatJID); err == nil {
		if alt := jid.ToNonAD().String(); alt != chatJID {
			keys = append(keys, alt+":"+msgID)
		}
	}
	return keys
}

// rememberPoll keeps a poll's option names under every key.
func (c *Channel) rememberPoll(chatJID, msgID string, names []string) {
	if len(names) == 0 {
		return
	}
	c.pollMu.Lock()
	defer c.pollMu.Unlock()
	for _, k := range pollKeys(chatJID, msgID) {
		if _, ok := c.polls[k]; !ok && len(c.polls) >= pollCacheLimit {
			for old := range c.polls {
				delete(c.polls, old)
				break
			}
		}
		c.polls[k] = names
	}
}

func (c *Channel) pollOptions(mk *waCommon.MessageKey) []string {
	c.pollMu.Lock()
	defer c.pollMu.Unlock()
	for _, k := range pollKeys(mk.GetRemoteJID(), mk.GetID()) {
		if names := c.polls[k]; len(names) > 0 {
			return names
		}
	}
	return nil
}

// rememberPollOption adds a user-added option to a known poll, so a vote
// for it still reads back as a name.
func (c *Channel) rememberPollOption(add *waE2E.PollAddOptionMessage) string {
	optName := strings.TrimSpace(add.GetAddOption().GetOptionName())
	if optName == "" {
		return "[Poll option added]"
	}
	mk := add.GetPollCreationMessageKey()
	c.pollMu.Lock()
	for _, k := range pollKeys(mk.GetRemoteJID(), mk.GetID()) {
		if names, ok := c.polls[k]; ok {
			c.polls[k] = append(names, optName)
		}
	}
	c.pollMu.Unlock()
	return "[Poll option added: " + optName + "]"
}

// decodeVote decrypts a vote and maps its option hashes back to names.
// ok is false when the vote could not be read in full.
func (c *Channel) decodeVote(ctx context.Context, evt *events.Message, mk *waCommon.MessageKey) (picked []string, ok bool) {
	client := c.clientCopy()
	if client == nil {
		return nil, false
	}
	vote, err := client.DecryptPollVote(ctx, evt)
	if err != nil {
		c.log.Debug("could not decrypt a WhatsApp poll vote", "error", err)
		return nil, false
	}
	options := c.pollOptions(mk)
	hashes := whatsmeow.HashPollOptions(options)
	picked = []string{}
	for _, sel := range vote.GetSelectedOptions() {
		for i, h := range hashes {
			if bytes.Equal(h, sel) {
				picked = append(picked, options[i])
				break
			}
		}
	}
	return picked, len(picked) == len(vote.GetSelectedOptions())
}
