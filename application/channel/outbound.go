package appchannel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// maxOutboundFiles bounds the files one reply sends to a chat.
const maxOutboundFiles = 6

// fileLinkRe matches [label](dest) and ![label](dest); fileURIRe matches a
// bare file:/// URI. A destination may hold spaces, quotes and angle
// brackets: agents copy paths verbatim from their tools.
var (
	fileLinkRe = regexp.MustCompile(`!?\[([^\]]{0,120})\]\(\s*([^()\n]+)\s*\)`)
	fileURIRe  = regexp.MustCompile(`file:///[^\s)\]"'<>` + "`" + `]+`)
)

// outbound is a reply ready for chats: the text as written, the files it
// links inside the room's folders, and the text without those links for
// chats that receive the files themselves.
type outbound struct {
	text     string
	stripped string
	files    []dservice.ChannelFile
}

// prepare reads the local files a reply links — markdown links to absolute
// paths and file:// URIs — when they lie inside the folders the room's
// turns work in, and the files an agent put in the reply itself (media).
// A link that does not resolve stays in the text.
func (a *App) prepare(ctx context.Context, convID, text string, media []valueobject.Attachment) outbound {
	out := outbound{text: text, stripped: text}
	if len(media) == 0 && !strings.Contains(text, "file://") && !strings.Contains(text, "](") {
		return out
	}
	ws, roots, err := a.rooms.Roots(ctx, appconversation.Place{ConversationID: convID})
	if err != nil || ws == nil {
		return out
	}
	sessions := map[string]drepo.WorkspaceSession{}
	defer func() {
		for _, s := range sessions {
			_ = s.Close()
		}
	}()
	sent := map[string]bool{} // accepted refs, as written
	read := map[string]bool{} // files already taken, by path
	take := func(ref, label string) {
		ref = strings.TrimSpace(strings.Trim(strings.TrimSpace(ref), `<>"'`))
		if sent[ref] {
			return
		}
		for _, p := range localPaths(ref) {
			if read[p] {
				sent[ref] = true
				return
			}
			if len(out.files) >= maxOutboundFiles {
				return
			}
			for _, root := range roots {
				rel, err := filepath.Rel(root, p)
				if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					continue
				}
				s := sessions[root]
				if s == nil {
					if s, err = ws.Open(ctx, root); err != nil {
						continue
					}
					sessions[root] = s
				}
				data, ok := readBounded(s, rel)
				if !ok {
					continue
				}
				name := filepath.Base(p)
				if label = strings.TrimSpace(label); label != "" && filepath.Ext(label) != "" {
					name = filepath.Base(label)
				}
				out.files = append(out.files, dservice.ChannelFile{Name: name, MediaType: http.DetectContentType(data), Data: data})
				read[p], sent[ref] = true, true
				return
			}
		}
	}
	// What the agent put in the reply goes first, in its order: a file in
	// the room's folders is read like a link, and one it sent inline, kept
	// as an upload, is read by its pinned path.
	for _, m := range media {
		if len(out.files) >= maxOutboundFiles {
			break
		}
		before := len(out.files)
		take(m.URI(), m.Name())
		if len(out.files) > before {
			continue
		}
		if data, ok := readPinned(ctx, ws, m); ok {
			out.files = append(out.files, dservice.ChannelFile{Name: m.Name(), MediaType: m.MediaType(), Data: data})
		}
	}
	for _, m := range fileLinkRe.FindAllStringSubmatch(text, 16) {
		take(m[2], m[1])
	}
	for _, u := range fileURIRe.FindAllString(text, 16) {
		take(u, "")
	}
	if len(sent) == 0 {
		return out
	}
	stripped := fileLinkRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := fileLinkRe.FindStringSubmatch(m)
		if !sent[strings.TrimSpace(strings.Trim(strings.TrimSpace(sub[2]), `<>"'`))] {
			return m
		}
		return sub[1]
	})
	out.stripped = strings.TrimSpace(fileURIRe.ReplaceAllStringFunc(stripped, func(m string) string {
		if sent[m] {
			return ""
		}
		return m
	}))
	return out
}

// localPaths are the absolute paths a file:// URI or a path may name: as
// written, then percent-decoded (markdown destinations are URLs, so agents
// sometimes encode a space).
func localPaths(ref string) []string {
	spellings := []string{ref}
	if strings.HasPrefix(ref, "file://") {
		u, err := url.Parse(ref)
		if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") {
			return nil
		}
		spellings = []string{u.Path}
	} else if dec, err := url.PathUnescape(ref); err == nil && dec != ref {
		spellings = append(spellings, dec)
	}
	var out []string
	for _, p := range spellings {
		if !filepath.IsAbs(p) {
			continue
		}
		if p = filepath.Clean(p); p != string(filepath.Separator) {
			out = append(out, p)
		}
	}
	return out
}

// readPinned reads a file an agent sent that the room keeps as an upload,
// outside the room's folders. Only a file whose bytes still match the hash
// recorded when it was kept is sent.
func readPinned(ctx context.Context, ws drepo.Workspace, m valueobject.Attachment) ([]byte, bool) {
	path, ok := strings.CutPrefix(m.URI(), "file://")
	if !ok || m.SHA256() == "" || !filepath.IsAbs(path) {
		return nil, false
	}
	r, info, err := ws.OpenHost(ctx, path)
	if err != nil {
		return nil, false
	}
	defer r.Close()
	if info.Size() == 0 || info.Size() > dservice.MaxChannelFile {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(r, dservice.MaxChannelFile+1))
	if err != nil || len(data) == 0 || len(data) > dservice.MaxChannelFile {
		return nil, false
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != m.SHA256() {
		return nil, false
	}
	return data, true
}

// readBounded reads a regular, non-empty file within the channel cap.
func readBounded(s drepo.WorkspaceSession, rel string) ([]byte, bool) {
	r, info, err := s.OpenRegular(rel)
	if err != nil {
		return nil, false
	}
	defer r.Close()
	if info.Size() == 0 || info.Size() > dservice.MaxChannelFile {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(r, dservice.MaxChannelFile+1))
	if err != nil || len(data) == 0 || len(data) > dservice.MaxChannelFile {
		return nil, false
	}
	return data, true
}

// sendReply delivers a prepared reply to one chat. A chat that takes files
// gets the text without their links and then each file; a file that does
// not go through is named in a note. Other chats get the text as written.
// It returns the chat's ids of the reply text and the files that went.
func (a *App) sendReply(ctx context.Context, chat valueobject.ChatRef, out outbound) ([]string, []dservice.ChannelFile, error) {
	ch, ok := a.Channel(chat.Channel())
	if !ok {
		return nil, nil, nil
	}
	fs, takesFiles := ch.(dservice.FileSender)
	if !takesFiles || len(out.files) == 0 {
		if out.text == "" {
			return nil, nil, nil // a reply of files only, for a chat that takes none
		}
		ids, err := ch.Send(ctx, chat, out.text)
		return ids, nil, err
	}
	var ids []string
	if out.stripped != "" {
		var err error
		if ids, err = ch.Send(ctx, chat, out.stripped); err != nil {
			return nil, nil, err
		}
	}
	var sent []dservice.ChannelFile
	var failed []string
	for _, f := range out.files {
		if err := fs.SendFile(ctx, chat, f); err != nil {
			a.log.Warn("could not send a file to a chat", "channel", chat.Channel(), "file", f.Name, "error", err)
			failed = append(failed, f.Name)
			continue
		}
		sent = append(sent, f)
	}
	if len(failed) > 0 {
		_, err := ch.Send(ctx, chat, "Could not send: "+strings.Join(failed, ", "))
		return ids, sent, err
	}
	return ids, sent, nil
}
