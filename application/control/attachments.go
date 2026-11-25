package appcontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/service/wire"
)

// A relayed message carries at most this many files, and this many bytes in
// total — the same shape the wire caps have, one layer up.
const (
	maxRelayAttachments = 4
	maxRelayBytes       = 32 << 20
)

// relayAttachments turns what the sender named into forms the target can
// resolve later, and refuses anything it could only resolve here:
//
//   - a local target gets an absolute path with its sha256 pin — the sender
//     pays a string, and the target pays the media tokens at its own provider
//     call, re-reading the file there;
//   - a remote (or unknown) target gets a stored copy, because a path there
//     names a file on another host;
//   - an http(s) reference passes through, because the provider fetches it and
//     copying would duplicate content NusaShell does not own.
//
// A relative path resolves inside the sender's workspace. Anything that is not
// a regular file, or is over the wire cap for its modality, is refused with the
// reason.
func (a *App) relayAttachments(ctx context.Context, target drepo.ConversationInfo, paths []string) (attachments []valueobject.Attachment, created []string, err error) {
	if len(paths) == 0 {
		return nil, nil, nil
	}
	if len(paths) > maxRelayAttachments {
		return nil, nil, apperror.New(400, fmt.Sprintf("a message carries at most %d attachments (%d given)", maxRelayAttachments, len(paths)), nil)
	}
	local := a.localTarget(ctx, target.ID)
	out := make([]valueobject.Attachment, 0, len(paths))
	// created names the copies this call stored: a send that is refused later
	// deletes exactly these, never another message's upload.
	created = make([]string, 0, len(paths))
	var total int64
	for _, raw := range paths {
		spec := strings.TrimSpace(raw)
		if spec == "" {
			return nil, created, apperror.New(400, "an attachment path is empty", nil)
		}
		if isRemoteURL(spec) {
			mediaType := mediaTypeFor(spec)
			att, err := valueobject.NewAttachment(spec, mediaType)
			if err != nil {
				return nil, created, apperror.New(400, "invalid attachment "+spec, err)
			}
			att.SetName(pathName(spec))
			out = append(out, att)
			continue
		}
		abs, err := a.senderPath(ctx, spec)
		if err != nil {
			return nil, created, err
		}
		info, err := os.Lstat(abs)
		if err != nil {
			return nil, created, apperror.New(400, "cannot read "+abs, err)
		}
		if !info.Mode().IsRegular() {
			return nil, created, apperror.New(400, abs+" is "+describeMode(info.Mode())+", not a regular file", nil)
		}
		mediaType := mediaTypeFor(abs)
		capBytes := wire.MaxAttachmentBytes(modalityOf(mediaType))
		if info.Size() > capBytes {
			return nil, created, apperror.New(413, fmt.Sprintf("%s is %s, over the %s cap for %s", filepath.Base(abs), humanBytes(info.Size()), humanBytes(capBytes), modalityOf(mediaType)), nil)
		}
		total += info.Size()
		if total > maxRelayBytes {
			return nil, created, apperror.New(413, fmt.Sprintf("the message's attachments are %s, over the %s per-message cap", humanBytes(total), humanBytes(maxRelayBytes)), nil)
		}
		att, id, err := a.relayFile(ctx, abs, mediaType, local)
		if err != nil {
			return nil, created, err
		}
		if id != "" {
			created = append(created, id)
		}
		out = append(out, att)
	}
	return out, created, nil
}

// relayFile builds the attachment for one regular file: a pinned path for a
// local target, a stored copy otherwise.
// relayFile builds the attachment for one regular file — a pinned path for a
// local target, a stored copy otherwise — and answers the copy's id so a
// refused send can take it back.
func (a *App) relayFile(ctx context.Context, abs, mediaType string, local bool) (valueobject.Attachment, string, error) {
	if local {
		pin, err := fileSHA256(abs)
		if err != nil {
			return valueobject.Attachment{}, "", apperror.New(400, "cannot read "+abs, err)
		}
		att, err := valueobject.NewAttachment("file://"+abs, mediaType)
		if err != nil {
			return valueobject.Attachment{}, "", apperror.New(400, "invalid attachment "+abs, err)
		}
		att.SetName(filepath.Base(abs))
		// The pin is what makes a path safe to hand over: the target re-reads
		// the file at every provider call, and a file that changed or vanished
		// is refused there instead of silently served as a different picture.
		att.SetSHA256(pin)
		return att, "", nil
	}
	if a.uploads == nil {
		return valueobject.Attachment{}, "", apperror.New(503, "attachments unavailable in this build", nil)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return valueobject.Attachment{}, "", apperror.New(400, "cannot read "+abs, err)
	}
	id, att, err := a.uploads.Save(ctx, filepath.Base(abs), mediaType, data)
	if err != nil {
		return valueobject.Attachment{}, "", err
	}
	return att, id, nil
}

// discardUploads takes back the copies this send stored when the message never
// made it: an upload a refusal left behind is an orphan until a delete-all, and
// a caller refused with "nothing was written" must find exactly that.
func (a *App) discardUploads(ids []string) {
	if a.uploads == nil || len(ids) == 0 {
		return
	}
	for _, id := range ids {
		if err := a.uploads.Delete(context.WithoutCancel(context.Background()), id); err != nil {
			a.log.Warn("could not take back an upload a refused send stored", "upload", id, "error", err)
		}
	}
}

// localTarget reports whether the target runs where the sender's paths mean
// something. An environment it cannot read is treated as remote — the fallback
// copies, because a path there would name another host's file.
func (a *App) localTarget(ctx context.Context, convID string) bool {
	env, err := a.rooms.Environment(ctx, convID)
	return err == nil && env == enum.EnvironmentLocal
}

// senderPath resolves what the sender typed into an absolute path: a relative
// path lives in the sender's workspace, and a file:// URI is the same path
// spelled the way stored attachments are.
func (a *App) senderPath(ctx context.Context, spec string) (string, error) {
	path := strings.TrimPrefix(spec, "file://")
	if !filepath.IsAbs(path) {
		root := service.WorkspaceDir(ctx)
		if root == "" {
			return "", apperror.New(400, "relative attachment "+spec+" needs a workspace to resolve in", nil)
		}
		path = filepath.Join(root, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", apperror.New(400, "invalid attachment path "+spec, err)
	}
	return abs, nil
}

// isRemoteURL reports a reference the provider fetches itself.
func isRemoteURL(spec string) bool {
	u, err := url.Parse(spec)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// pathName is the file name a URL or path ends with, for display.
func pathName(spec string) string {
	if u, err := url.Parse(spec); err == nil && u.Path != "" {
		spec = u.Path
	}
	return filepath.Base(spec)
}

// mediaTypeFor infers an attachment's media type from its name. Unknown
// extensions stay a file: the wire cap and the model's capability both key on
// this, and guessing "image" for a file that is not one is worse than saying
// nothing.
func mediaTypeFor(spec string) string {
	if mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(spec))); mt != "" {
		if base, _, err := mime.ParseMediaType(mt); err == nil {
			return base
		}
	}
	return "application/octet-stream"
}

// modalityOf mirrors Attachment.Modality for a bare media type.
func modalityOf(mediaType string) enum.Modality {
	switch top, _, _ := strings.Cut(mediaType, "/"); top {
	case "image":
		return enum.ModalityImage
	case "audio":
		return enum.ModalityAudio
	case "video":
		return enum.ModalityVideo
	case "text":
		return enum.ModalityText
	}
	return enum.ModalityFile
}

// fileSHA256 pins a file's current bytes.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// describeMode names what a refused path actually is.
func describeMode(mode os.FileMode) string {
	switch {
	case mode.IsDir():
		return "a directory"
	case mode&os.ModeDevice != 0:
		return "a device"
	case mode&os.ModeNamedPipe != 0:
		return "a FIFO"
	case mode&os.ModeSocket != 0:
		return "a socket"
	}
	return "not a regular file"
}

// humanBytes renders a size the way a refusal reads best.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

var _ = errors.Is
