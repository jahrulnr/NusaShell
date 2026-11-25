// Package wire holds wire-neutral helpers shared by adapter families —
// URI resolution, base64 materialization. Wire-shaped part assembly stays
// inside each adapter; only the mechanics that never change per wire live
// here.
package wire

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// MaxAttachmentBytes returns the largest local media payload materialized by
// a tool or provider adapter for the given modality.
func MaxAttachmentBytes(modality enum.Modality) int64 {
	switch modality {
	case enum.ModalityImage:
		return 10 << 20
	case enum.ModalityAudio:
		return 50 << 20
	case enum.ModalityVideo:
		return 100 << 20
	default:
		return 20 << 20
	}
}

// MediaRoot binds a provider send to the workspace repository chosen at
// composition time. Empty Dir disables local file references.
type MediaRoot struct {
	Dir   string
	Store drepo.Workspace
	// Readers names the tools that can read an attachment the target model
	// cannot take, by modality. The placeholder a dropped file leaves behind
	// points at the one that can actually help — read_media for images,
	// transcribe for audio, nothing for video.
	Readers Readers
}

// Readers names the tools that can read an attachment the target model cannot
// take. A modality with no reader is named by no tool: pointing a video at a
// tool that cannot hand it over would be worse than silence.
type Readers struct {
	Image string
	Audio string
}

// For returns the tool that can read mod, "" when nothing can help.
func (r Readers) For(mod enum.Modality) string {
	switch mod {
	case enum.ModalityImage:
		return r.Image
	case enum.ModalityAudio:
		return r.Audio
	}
	return ""
}

// Omitted is the placeholder that stands in for an attachment the target
// model cannot read: it names the file and why it was dropped, and — when a
// reader can help with that modality — the tool that can. A dropped file is
// never silent: the model either asks the right tool or, with no reader, still
// learns which file it is missing.
func Omitted(a valueobject.Attachment, readers Readers) string {
	modality := a.Modality().String()
	var b strings.Builder
	b.WriteString("(" + modality)
	if name := safeName(a.Name()); name != "" {
		b.WriteString(" " + strconv.Quote(name))
	}
	b.WriteString(" omitted: model does not accept " + pluralModality(a.Modality()))
	if reader := readers.For(a.Modality()); reader != "" {
		b.WriteString("; " + reader + " can read it")
	}
	b.WriteString(")")
	return b.String()
}

// pluralModality names the modality as a denominal object: "images" and
// "files" read as classes, "audio" and "video" as mass nouns.
func pluralModality(mod enum.Modality) string {
	switch mod {
	case enum.ModalityImage:
		return "images"
	case enum.ModalityFile:
		return "files"
	}
	return mod.String()
}

// safeName reduces a stored attachment name to one printable path base —
// the name lands in a prompt, so newlines and quotes must not survive.
func safeName(name string) string {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	if name == "." || name == "/" {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		if r >= 0x20 && r != 0x7f && r != '"' && r != '\'' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ResolveURI materializes an attachment reference into a sendable URL. Local
// file references require fileRoot and are reopened beneath it at send time;
// reads are bounded before allocation and a sha256 pin must still match.
func ResolveURI(a valueobject.Attachment, fileRoot MediaRoot) (string, error) {
	uri, mt := a.URI(), a.MediaType()
	if strings.HasPrefix(uri, "file://") {
		raw, err := readLocal(a, fileRoot)
		if err != nil {
			return "", err
		}
		if a.Modality() == enum.ModalityImage {
			raw, mt, err = prepareImage(raw)
			if err != nil {
				return "", err
			}
		}
		return DataURI(mt, raw), nil
	}
	if strings.HasPrefix(uri, "data:") {
		marker := strings.Index(uri, ";base64,")
		if marker >= 0 && len(uri)-marker-len(";base64,") > base64.StdEncoding.EncodedLen(int(MaxAttachmentBytes(a.Modality()))) {
			return "", apperror.New(400, "attachment exceeds media limit", nil)
		}
		if a.SHA256() != "" || a.Modality() == enum.ModalityImage {
			raw, err := decodeDataURI(uri)
			if err != nil {
				return "", err
			}
			if int64(len(raw)) > MaxAttachmentBytes(a.Modality()) {
				return "", apperror.New(400, "attachment exceeds media limit", nil)
			}
			if err := verifyPin(a, raw); err != nil {
				return "", err
			}
			if a.Modality() == enum.ModalityImage {
				raw, mt, err = prepareImage(raw)
				if err != nil {
					return "", err
				}
				return DataURI(mt, raw), nil
			}
		}
		return uri, nil
	}
	if a.SHA256() != "" {
		return "", apperror.New(400, "pinned attachment must be a local reference",
			fmt.Errorf("cannot verify sha256 of remote uri"))
	}
	return uri, nil
}

func readLocal(a valueobject.Attachment, fileRoot MediaRoot) (raw []byte, err error) {
	if fileRoot.Dir == "" || fileRoot.Store == nil {
		return nil, apperror.New(400, "local attachment root is not configured", nil)
	}
	absRoot, err := filepath.Abs(fileRoot.Dir)
	if err != nil {
		return nil, err
	}
	absPath, err := filepath.Abs(strings.TrimPrefix(a.URI(), "file://"))
	if err != nil {
		return nil, err
	}
	var f io.ReadCloser
	var info fs.FileInfo
	relative, relErr := filepath.Rel(absRoot, absPath)
	inside := relErr == nil && relative != ".." && !filepath.IsAbs(relative) &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
	if inside {
		// Workspace paths are slash-separated on every host; Rel emits host
		// separators, so normalize before the workspace grammar validates it.
		workspace, err := valueobject.NewWorkspace(filepath.ToSlash(relative))
		if err != nil {
			return nil, apperror.New(400, "attachment outside workspace", err)
		}
		root, err := fileRoot.Store.Open(context.Background(), absRoot)
		if err != nil {
			return nil, err
		}
		defer func() {
			if closeErr := root.Close(); err == nil {
				err = closeErr
			}
		}()
		f, info, err = root.OpenRegular(workspace.Path())
		if err != nil {
			return nil, apperror.New(400, "attachment unreadable", err)
		}
	} else {
		// Outside the root only a sha256-pinned reference may cross: pins
		// are minted by tools at read time, so the file hashed then is
		// provably what send-time serves. An unmediated file:// outside
		// the workspace stays rejected.
		if a.SHA256() == "" {
			return nil, apperror.New(400, "attachment outside workspace", relErr)
		}
		f, info, err = fileRoot.Store.OpenHost(context.Background(), absPath)
		if err != nil {
			return nil, apperror.New(400, "attachment unreadable", err)
		}
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}()
	limit := MaxAttachmentBytes(a.Modality())
	if info.Size() > limit {
		return nil, apperror.New(400, "attachment exceeds media limit", nil)
	}
	raw, err = io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, apperror.New(400, "attachment unreadable", err)
	}
	if int64(len(raw)) > limit {
		return nil, apperror.New(400, "attachment exceeds media limit", nil)
	}
	if err := verifyPin(a, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// ErrPinChanged marks an attachment whose bytes no longer match the digest its
// producer pinned: the caller decides what a changed file means, but every path
// must refuse to serve it.
var ErrPinChanged = errors.New("attachment content changed")

// verifyPin enforces the optional content pin: resolved bytes must hash
// to the digest the producer recorded at check time.
func verifyPin(a valueobject.Attachment, raw []byte) error {
	if a.SHA256() == "" {
		return nil
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != a.SHA256() {
		return apperror.New(400, "attachment content changed",
			fmt.Errorf("%w: got %s, want %s", ErrPinChanged, got, a.SHA256()))
	}
	return nil
}

// VerifyPinnedFile re-checks a local attachment's content pin without
// materializing what it will send. The inline path verifies while it reads; a
// reference handed to an agent is read by the agent itself, so the pin has to
// be checked before the reference crosses — one rule, both paths, or a changed
// file is served as a different picture through the other door.
//
// An unpinned reference and a remote URI have nothing to verify here: a remote
// reference is the provider's to fetch, and an unpinned file never claimed a
// hash.
func VerifyPinnedFile(a valueobject.Attachment) error {
	uri := a.URI()
	if !strings.HasPrefix(uri, "file://") || a.SHA256() == "" {
		return nil
	}
	path := strings.TrimPrefix(uri, "file://")
	f, err := os.Open(path)
	if err != nil {
		return apperror.New(400, "attachment unreadable", err)
	}
	defer f.Close()
	limit := MaxAttachmentBytes(a.Modality())
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return apperror.New(400, "attachment unreadable", err)
	}
	if int64(len(raw)) > limit {
		return apperror.New(400, "attachment exceeds media limit", nil)
	}
	return verifyPin(a, raw)
}

// decodeDataURI extracts the base64 payload of a data: URI.
func decodeDataURI(uri string) ([]byte, error) {
	_, after, ok := strings.Cut(uri, ";base64,")
	if !ok {
		return nil, apperror.New(400, "invalid data URI",
			fmt.Errorf("missing ;base64, marker"))
	}
	raw, err := base64.StdEncoding.DecodeString(after)
	if err != nil {
		return nil, apperror.New(400, "invalid data URI", err)
	}
	return raw, nil
}

// DataURI assembles data:<mime>;base64,<raw> — the inline-payload form
// most chat wires accept for media.
func DataURI(mediaType string, raw []byte) string {
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(raw)
}

// ResolveBase64 splits a data: URI (or resolves file:// into one) into its
// raw base64 payload and the media type of that payload — what wires that
// take media as separate fields expect instead of a URL. The media type is
// the payload's own, which differs from the attachment's when an image was
// sent smaller. Audio over plain http(s) is rejected: nothing to embed
// without fetching, and adapters do not fetch arbitrary URLs.
func ResolveBase64(a valueobject.Attachment, fileRoot MediaRoot) (data, mediaType string, err error) {
	uri := a.URI()
	if !strings.HasPrefix(uri, "data:") && !strings.HasPrefix(uri, "file://") {
		return "", "", apperror.New(400, "attachment must be a data: or file:// URI",
			fmt.Errorf("unsupported uri scheme: %s", uri))
	}
	u, err := ResolveURI(a, fileRoot)
	if err != nil {
		return "", "", err
	}
	header, data, ok := strings.Cut(u, ";base64,")
	if !ok {
		return "", "", apperror.New(400, "invalid data URI",
			fmt.Errorf("missing ;base64, marker"))
	}
	return data, strings.TrimPrefix(header, "data:"), nil
}

// FormatName maps a MIME subtype to the format token wires expect:
// audio/mpeg → mp3, audio/x-wav → wav; anything else stays verbatim.
func FormatName(mediaType string) string {
	sub := mediaType[strings.IndexByte(mediaType, '/')+1:]
	switch sub {
	case "mpeg":
		return "mp3"
	case "x-wav", "wave":
		return "wav"
	default:
		return sub
	}
}
