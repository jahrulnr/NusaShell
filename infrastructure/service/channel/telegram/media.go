package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

const (
	// photoLimitBytes is where sendPhoto stops and sendDocument takes over.
	photoLimitBytes = 10 << 20
	// downloadLimitBytes is the Bot API's getFile ceiling. A larger declared
	// file is reported instead of being requested at all.
	downloadLimitBytes = 20 << 20
	// albumFileLimit is what one album buffers; the room takes no more
	// files from one message.
	albumFileLimit = 6
	// albumDebounce is how long a media group waits for its last item.
	// Telegram sends every album item as its own update, milliseconds apart.
	albumDebounce = 400 * time.Millisecond
)

var _ dservice.FileSender = (*Channel)(nil)

// SendFile uploads one file to the chat: GIFs as animations, images up to
// 10 MB as photos, video as video, OGG audio as a voice note, other audio
// as audio, and everything else as a document.
func (c *Channel) SendFile(ctx context.Context, chat valueobject.ChatRef, f dservice.ChannelFile) error {
	chatID, threadID, err := parseChat(chat)
	if err != nil {
		return err
	}
	if len(f.Data) == 0 {
		return errors.New("the file is empty")
	}
	if len(f.Data) > dservice.MaxChannelFile {
		return fmt.Errorf("the file is larger than %d MB", dservice.MaxChannelFile>>20)
	}
	client := c.clientCopy()
	if client == nil {
		return errors.New("Telegram is not connected")
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(f.MediaType, ";")[0]))
	if mediaType == "" {
		mediaType = http.DetectContentType(f.Data)
	}
	method, field := mediaMethod(mediaType, len(f.Data))
	fields := map[string]string{"chat_id": strconv.FormatInt(chatID, 10)}
	if threadID != 0 {
		fields["message_thread_id"] = strconv.FormatInt(threadID, 10)
	}
	_, err = client.sendMedia(ctx, method, field, f.Name, mediaType, f.Data, fields)
	if wait, limited := retryAfterDelay(err); limited {
		sleep(ctx, wait)
		_, err = client.sendMedia(ctx, method, field, f.Name, mediaType, f.Data, fields)
	}
	return err
}

// mediaMethod maps a media type and size to the Bot API send method and
// the multipart field that carries the bytes.
func mediaMethod(mediaType string, size int) (method, field string) {
	switch {
	case mediaType == "image/gif":
		return "sendAnimation", "animation"
	case strings.HasPrefix(mediaType, "image/"):
		if size <= photoLimitBytes {
			return "sendPhoto", "photo"
		}
		return "sendDocument", "document"
	case strings.HasPrefix(mediaType, "video/"):
		return "sendVideo", "video"
	case mediaType == "audio/ogg":
		return "sendVoice", "voice"
	case strings.HasPrefix(mediaType, "audio/"):
		return "sendAudio", "audio"
	default:
		return "sendDocument", "document"
	}
}

/* ---------------- inbound media ---------------- */

// mediaCandidate is one file-shaped payload found on a message.
type mediaCandidate struct {
	kind       string // marker noun: Image, Document, Voice note, …
	stem       string // fallback file-name stem
	mediaType  string
	fileName   string
	fileID     string
	fileSize   int64
	defaultExt string
}

// mediaCandidates lists the media a message carries, in the order
// Telegram nests them.
func mediaCandidates(m *tgMessage) []mediaCandidate {
	var out []mediaCandidate
	seen := map[string]bool{}
	add := func(c mediaCandidate) {
		if c.fileID == "" || seen[c.fileID] {
			return
		}
		seen[c.fileID] = true
		out = append(out, c)
	}
	if len(m.Photo) > 0 {
		// Telegram orders photo renditions ascending; the last is largest.
		largest := m.Photo[len(m.Photo)-1]
		add(mediaCandidate{kind: "Image", stem: "photo", fileID: largest.FileID,
			fileSize: largest.FileSize, mediaType: "image/jpeg", defaultExt: ".jpg"})
	}
	addFile := func(kind, stem string, f *tgMedia, defExt string) {
		if f == nil {
			return
		}
		add(mediaCandidate{kind: kind, stem: stem, fileID: f.FileID, fileSize: f.FileSize,
			mediaType: f.MIME, fileName: strings.TrimSpace(f.FileName), defaultExt: defExt})
	}
	addFile("Document", "document", m.Document, "")
	addFile("Video", "video", m.Video, ".mp4")
	addFile("Audio", "audio", m.Audio, ".mp3")
	addFile("Voice note", "voice", m.Voice, ".ogg")
	addFile("Video note", "video-note", m.VideoNote, ".mp4")
	addFile("Animation", "animation", m.Animation, ".mp4")
	addFile("Sticker", "sticker", m.Sticker, ".webp")
	return out
}

// download fetches one candidate. The marker is the text to add when the
// file did not arrive; the file is valid only when the marker is empty.
func (c *Channel) download(ctx context.Context, client *apiClient, m *tgMessage, cand mediaCandidate) (dservice.ChannelFile, string) {
	// getFile plus the transfer gets its own bound; the poll loop's context
	// lives for the whole connection.
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	tooLarge := "[" + cand.kind + " is larger than 20 MB and was not downloaded]"
	failed := "[" + cand.kind + " could not be downloaded]"
	if cand.fileSize > downloadLimitBytes {
		return dservice.ChannelFile{}, tooLarge
	}
	file, err := client.getFile(ctx, cand.fileID)
	if err != nil {
		c.log.Warn("could not look up a Telegram file", "kind", cand.kind, "error", err)
		return dservice.ChannelFile{}, failed
	}
	if file.FileSize > downloadLimitBytes {
		return dservice.ChannelFile{}, tooLarge
	}
	data, err := client.downloadFile(ctx, file.FilePath, dservice.MaxChannelFile)
	if err != nil {
		c.log.Warn("could not download a Telegram file", "kind", cand.kind, "error", err)
		return dservice.ChannelFile{}, failed
	}
	mediaType := strings.TrimSpace(strings.Split(cand.mediaType, ";")[0])
	if mediaType == "" {
		mediaType = http.DetectContentType(data)
	}
	fileName := cand.fileName
	if fileName == "" {
		fileName = cand.stem + "-" + strconv.FormatInt(m.MessageID, 10) + extensionFor(mediaType, cand.defaultExt)
	}
	return dservice.ChannelFile{Name: filepath.Base(fileName), MediaType: mediaType, Data: data}, ""
}

// extensionFor gives unnamed media a sane suffix instead of a bare id.
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
	case "audio/ogg":
		return ".ogg"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/mp4":
		return ".m4a"
	case "application/pdf":
		return ".pdf"
	}
	return fallback
}

// joinText appends one more block without repeating identical text — for
// captions and failure markers.
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

/* ---------------- albums ---------------- */

// albumBatch gathers one media group. gen invalidates a timer that fires
// just as a newer item arrives.
type albumBatch struct {
	gen   uint64
	first dservice.InboundMessage
	texts []string
	files []dservice.ChannelFile
	timer *time.Timer
}

func albumKey(m *tgMessage) string {
	return strconv.FormatInt(m.Chat.ID, 10) + ":" + strconv.FormatInt(m.MessageThreadID, 10) + ":" + m.MediaGroupID
}

// bufferAlbum adds one item to its group and restarts the debounce. The
// merged message keeps the first item's id, so dedupe stays chat:message.
func (c *Channel) bufferAlbum(m *tgMessage, in dservice.InboundMessage) {
	key := albumKey(m)
	c.albumMu.Lock()
	defer c.albumMu.Unlock()
	b := c.albums[key]
	if b == nil {
		b = &albumBatch{first: in}
		c.albums[key] = b
	}
	b.gen++
	gen := b.gen
	if room := albumFileLimit - len(b.files); room > 0 && len(in.Files) > 0 {
		b.files = append(b.files, in.Files[:min(len(in.Files), room)]...)
	}
	if t := strings.TrimSpace(in.Text); t != "" {
		b.texts = append(b.texts, t)
	}
	if b.timer != nil {
		b.timer.Stop()
	}
	b.timer = time.AfterFunc(albumDebounce, func() { c.flushAlbum(key, gen) })
}

// flushAlbum delivers one settled group, unless a newer item superseded
// the timer that scheduled this call.
func (c *Channel) flushAlbum(key string, gen uint64) {
	c.albumMu.Lock()
	b := c.albums[key]
	if b == nil || b.gen != gen {
		c.albumMu.Unlock()
		return
	}
	delete(c.albums, key)
	c.albumMu.Unlock()
	c.dispatchAlbum(b)
}

// flushAlbums delivers every buffered group, so an album that arrived just
// before polling stopped is not lost.
func (c *Channel) flushAlbums() {
	c.albumMu.Lock()
	batches := make([]*albumBatch, 0, len(c.albums))
	for key, b := range c.albums {
		if b.timer != nil {
			b.timer.Stop()
		}
		delete(c.albums, key)
		batches = append(batches, b)
	}
	c.albumMu.Unlock()
	for _, b := range batches {
		c.dispatchAlbum(b)
	}
}

func (c *Channel) dispatchAlbum(b *albumBatch) {
	in := b.first
	in.Text = strings.TrimSpace(strings.Join(b.texts, "\n"))
	in.Files = b.files
	if in.Text == "" && len(in.Files) == 0 {
		return
	}
	c.mu.Lock()
	handler := c.handler
	c.mu.Unlock()
	if handler != nil {
		handler.HandleInbound(context.Background(), in)
	}
}
