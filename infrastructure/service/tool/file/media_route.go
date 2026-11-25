package toolfile

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	"github.com/jahrulnr/nusashell/infrastructure/service/wire"
)

const (
	// descriptionPromptVersion names the question asked; it rides the cache
	// key, so rewording the prompt never reuses an answer given to the old one.
	descriptionPromptVersion = "1"
	// descriptionPrompt is what the vision model is asked for. A description
	// is orientation for a model that cannot see: what is shown, every word
	// of text, and whatever the owner could act on.
	descriptionPrompt = "Describe this image for a model that cannot see it. Say what it shows, transcribe every piece of text exactly as written and in reading order, and note anything actionable: UI state, error messages, code, table values, diagram labels. Be concrete; do not speculate beyond the image."
)

// Transcriber turns speech in an audio payload into text — the media use
// case's transcription role implements it.
type Transcriber interface {
	Transcribe(ctx context.Context, req dservice.TranscriptionRequest) (string, error)
	// Identity names the model that will read the audio, "<provider>/<model>".
	// A third party's answer belongs to the model that gave it, so a transcript
	// is cached under that identity: without it, switching models would serve
	// the previous reader's words. "" or an error is an unknown reader, and the
	// key says so explicitly instead of pretending to know.
	Identity(ctx context.Context) (string, error)
}

// MediaDeps is what a file tool needs to route a payload the turn's model
// cannot read: the vision bridge that describes an image, the transcription
// role that reads audio, and the per-conversation cache their answers live
// in. A nil bridge or transcriber only matters when that branch is taken.
type MediaDeps struct {
	Descriptions dservice.MediaDescriptions
	Vision       dservice.Vision
	Transcriber  Transcriber
}

// mediaRoute is the path a file tool takes for a payload that cannot go back
// as text: attach it when the turn's model accepts the modality, or read it
// for the model when it cannot — an image is described by the vision role,
// audio is transcribed. One implementation serves read_file and read_media,
// so the two names cannot disagree about what the harness does with a file,
// and neither asks the model to know its own modality support.
type mediaRoute struct {
	workspace drepo.Workspace
	root      string
	media     MediaDeps
}

// attachOrRead loads path — absolute anywhere on disk, or relative to the
// turn's workspace — and routes it by what the turn's model can read. Text
// goes back as read_file renders it (a first page, since this call carries no
// paging inputs); anything else is attached, or read for the model.
func (r mediaRoute) attachOrRead(ctx context.Context, call entity.ToolCall, path string) (result dservice.ToolResult, retErr error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return dservice.ToolResult{}, errors.New("path is required")
	}
	expanded, err := expandPath(path)
	if err != nil {
		return dservice.ToolResult{}, err
	}
	path = expanded
	var f io.ReadCloser
	var info fs.FileInfo
	var uri string
	if filepath.IsAbs(path) {
		file, stat, err := r.workspace.OpenHost(ctx, path)
		if err != nil {
			return dservice.ToolResult{}, fmt.Errorf("%q is not a readable regular file: %w", path, err)
		}
		f, info, uri = file, stat, r.workspace.HostURI(path)
	} else {
		root, rel, err := callRoot(ctx, r.workspace, r.root, path)
		if err != nil {
			return dservice.ToolResult{}, err
		}
		defer func() {
			if closeErr := root.Close(); retErr == nil {
				retErr = closeErr
			}
		}()
		file, stat, err := root.OpenRegular(rel)
		if err != nil {
			return dservice.ToolResult{}, fmt.Errorf("%q is not a readable regular file: %w", path, err)
		}
		f, info, uri = file, stat, root.AttachmentURI(rel)
	}
	defer func() {
		if closeErr := f.Close(); retErr == nil {
			retErr = closeErr
		}
	}()
	// Sniff the first 512 bytes — DetectContentType's window — and let the
	// shared rule settle what the bytes cannot say alone (a .m4a sniffs as
	// video, an aac stream as octet-stream). The same rule the media tools
	// use, so a voice note transcribes and reads alike.
	prefix := make([]byte, 512)
	n, err := io.ReadFull(f, prefix)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return dservice.ToolResult{}, err
	}
	prefix = prefix[:n]
	mediaType := dservice.SniffMediaType(prefix, path)
	att, err := valueobject.NewAttachment(uri, mediaType)
	if err != nil {
		return dservice.ToolResult{}, err
	}
	modality := att.Modality()
	if modality == enum.ModalityText {
		return r.readText(call, prefix, f, info, path)
	}
	// Cap by modality from the stat, before the reference is handed out —
	// adapters resolve file:// by reading the file at send time, so an
	// oversized payload would otherwise fail late and less legibly.
	limit := wire.MaxAttachmentBytes(modality)
	if info.Size() > limit {
		return dservice.ToolResult{}, fmt.Errorf("%q exceeds the %d MiB limit for %s media", path, limit>>20, modality)
	}
	if accepted, known := dservice.ModelAccepts(ctx, modality); known && !accepted {
		return r.readForModel(ctx, path, mediaType, modality, prefix, f, limit)
	}
	return r.attach(path, att, prefix, f, info, limit, modality)
}

// readText renders a payload the sniff called text the way read_file does:
// UTF-8 checked and paged to the call's output budget. read_media carries no
// paging inputs, so it answers with the first page and the note read_file
// would have left.
func (r mediaRoute) readText(call entity.ToolCall, prefix []byte, f io.Reader, info fs.FileInfo, path string) (dservice.ToolResult, error) {
	if info.Size() > readFileMax {
		return dservice.ToolResult{}, fmt.Errorf("%q is too large to page through (%d bytes > %d MiB) — read parts of it with exec (sed -n, head, tail)", path, info.Size(), readFileMax>>20)
	}
	rest, err := io.ReadAll(io.LimitReader(f, readFileMax+1-int64(len(prefix))))
	if err != nil {
		return dservice.ToolResult{}, err
	}
	raw := append(append([]byte{}, prefix...), rest...)
	if len(raw) > readFileMax {
		return dservice.ToolResult{}, fmt.Errorf("%q is too large to page through (> %d MiB)", path, readFileMax>>20)
	}
	if !utf8.Valid(raw) {
		return dservice.ToolResult{}, fmt.Errorf("%q is not valid UTF-8 — convert it first (e.g. iconv via exec)", path)
	}
	text, err := readPage(string(raw), 1, 0, call.OutputBudget())
	if err != nil {
		return dservice.ToolResult{}, fmt.Errorf("%q: %w", path, err)
	}
	return dservice.ToolResult{Text: text}, nil
}

// attach hands the payload to the model as a stored reference, pinned to the
// bytes hashed through this same confined handle so send-time resolution
// verifies exactly what the model was promised.
func (r mediaRoute) attach(path string, att valueobject.Attachment, prefix []byte, f io.Reader, info fs.FileInfo, limit int64, modality enum.Modality) (dservice.ToolResult, error) {
	h := sha256.New()
	_, _ = h.Write(prefix)
	rest, err := io.Copy(h, io.LimitReader(f, limit+1-int64(len(prefix))))
	if err != nil {
		return dservice.ToolResult{}, err
	}
	if int64(len(prefix))+rest > limit {
		return dservice.ToolResult{}, fmt.Errorf("%q exceeds the %d MiB limit for %s media", path, limit>>20, modality)
	}
	att.SetSHA256(hex.EncodeToString(h.Sum(nil)))
	att.SetName(filepath.Base(path))
	return dservice.ToolResult{
		Text:        fmt.Sprintf("loaded %s %s (%s, %d bytes) — it is now attached for the model", modality, att.Name(), att.MediaType(), info.Size()),
		Attachments: []valueobject.Attachment{att},
	}, nil
}

// readForModel answers for a model that cannot take the modality: an image is
// described by the vision role, audio is transcribed by the transcription
// role, and every other kind is refused with what the harness can and cannot
// do — never silently dropped. Both readings sit under one send consent, so
// the owner has one switch to turn on, not one per modality.
func (r mediaRoute) readForModel(ctx context.Context, path, mediaType string, modality enum.Modality, prefix []byte, f io.Reader, limit int64) (dservice.ToolResult, error) {
	raw, err := readRest(prefix, f, limit)
	if err != nil {
		return dservice.ToolResult{}, err
	}
	if r.media.Vision == nil {
		return dservice.ToolResult{}, errors.New("no reading bridge is wired in this build — the file cannot be read for the model")
	}
	reading, err := r.media.Vision.ReadSettings(ctx)
	if err != nil {
		return dservice.ToolResult{}, fmt.Errorf("could not read the media reading settings: %w", err)
	}
	switch modality {
	case enum.ModalityImage:
		return r.describe(ctx, path, mediaType, raw, reading)
	case enum.ModalityAudio:
		return r.transcribe(ctx, path, mediaType, raw, reading)
	}
	return dservice.ToolResult{}, fmt.Errorf("%q is %s and the turn's model does not accept it — nothing can hand %s to this model", path, modality, modality)
}

// describe reads an image with the vision role. The owner's two decisions come
// first (the role names the reader, the switch allows the send), then the
// assigned model's own capability: nothing leaves the machine before all three
// agree.
func (r mediaRoute) describe(ctx context.Context, path, mediaType string, raw []byte, reading dservice.VisionReading) (dservice.ToolResult, error) {
	if reading.ProviderID == "" || reading.ModelID == "" {
		return dservice.ToolResult{}, errors.New("no vision model is set — choose a provider and model for models.vision in Settings → Models so read_media (or read_file) can describe images")
	}
	if !reading.Consent {
		return dservice.ToolResult{}, fmt.Errorf(
			"sending files to %s / %s is off — turn on media.reading_consent in Settings → Models to let read_media or read_file send this image (the switch covers image descriptions and audio transcripts)",
			reading.ProviderID, reading.ModelID)
	}
	target, err := r.media.Vision.ResolveModel(ctx, reading.ProviderID, reading.ModelID)
	if err != nil {
		return dservice.ToolResult{}, err
	}
	if !target.Model.AcceptsInput(enum.ModalityImage) {
		return dservice.ToolResult{}, fmt.Errorf(
			"the vision model %s on %s does not accept images — choose a model that declares image input for models.vision",
			target.Model.ID(), target.Provider.ID())
	}
	key := descriptionKey(raw, reading.ProviderID, reading.ModelID)
	if text, ok := r.cached(ctx, key); ok {
		return dservice.ToolResult{Text: text}, nil
	}
	text, err := describeImage(ctx, target, mediaType, raw, filepath.Base(path))
	if err != nil {
		return dservice.ToolResult{}, err
	}
	r.remember(ctx, key, text)
	return dservice.ToolResult{Text: text}, nil
}

// transcribe reads audio with the transcription role. The switch is the same
// one that gates descriptions — a transcript sends the file to a third party
// exactly as a description does — and the role names who would read it. The
// transcript is text, like a description, so it never counts toward an image
// cap and never appears as a file.
func (r mediaRoute) transcribe(ctx context.Context, path, mediaType string, raw []byte, reading dservice.VisionReading) (dservice.ToolResult, error) {
	if !reading.Consent {
		return dservice.ToolResult{}, errors.New("sending audio to the transcription model is off — turn on media.reading_consent in Settings → Models to let read_media or read_file transcribe it (the switch covers image descriptions and audio transcripts)")
	}
	if r.media.Transcriber == nil {
		return dservice.ToolResult{}, errors.New("no transcription models are wired in this build — the audio cannot be transcribed")
	}
	key := transcriptionKey(raw, r.transcriberIdentity(ctx))
	if text, ok := r.cached(ctx, key); ok {
		return dservice.ToolResult{Text: text}, nil
	}
	text, err := r.media.Transcriber.Transcribe(ctx, dservice.TranscriptionRequest{
		Audio: dservice.Media{MediaType: mediaType, Data: raw},
	})
	if err != nil {
		return dservice.ToolResult{}, err
	}
	if strings.TrimSpace(text) == "" {
		return dservice.ToolResult{Text: "(no speech was heard)"}, nil
	}
	text = strings.TrimSpace(text)
	r.remember(ctx, key, text)
	return dservice.ToolResult{Text: text}, nil
}

// cached reads one room's stored answer. An unreadable entry is a cold
// cache: describe the image again rather than fail a call the owner asked for.
func (r mediaRoute) cached(ctx context.Context, key string) (string, bool) {
	convID := dservice.ConversationID(ctx)
	if convID == "" || r.media.Descriptions == nil {
		return "", false
	}
	text, ok, err := r.media.Descriptions.Get(convID, key)
	return text, ok && err == nil
}

// remember stores the answer for this room. A cache that cannot write is a
// cold cache, not a failed description.
func (r mediaRoute) remember(ctx context.Context, key, text string) {
	convID := dservice.ConversationID(ctx)
	if convID == "" || r.media.Descriptions == nil {
		return
	}
	_ = r.media.Descriptions.Put(convID, key, text)
}

// transcriptionKey fingerprints one transcript: the audio bytes, the reader's
// identity, and the prompt version. A transcript belongs to the model that
// produced it — the same audio read by another model is another answer — so
// the identity rides the key; an unknown reader is the explicit
// "unknown" marker, never a key that silently omits the field.
func transcriptionKey(content []byte, identity string) string {
	sum := sha256.New()
	sum.Write(content)
	sum.Write([]byte("\x00transcribe\x00" + identity + "\x00" + descriptionPromptVersion))
	return hex.EncodeToString(sum.Sum(nil))
}

// unknownIdentity marks a transcript whose reader could not be named. It
// starts with NUL so no real identity — a trimmed "provider/model" — can ever
// equal it, and the key still carries an explicit marker instead of omitting
// the field. A plausible-looking sentinel ("unknown") would be a trap for the
// next implementation: a model or provider may well be named that.
const unknownIdentity = "\x00unknown"

// transcriberIdentity names the reader the transcript will belong to. An
// unreadable or unnamed role is the explicit unknown marker: the cache must be
// able to tell one reader from another, and silence would hide a wiring gap.
func (r mediaRoute) transcriberIdentity(ctx context.Context) string {
	identity, err := r.media.Transcriber.Identity(ctx)
	if err != nil || strings.TrimSpace(identity) == "" {
		return unknownIdentity
	}
	return strings.TrimSpace(identity)
}

// descriptionKey fingerprints one description: the image bytes, the model
// that read it, and the prompt version that asked. A changed model or prompt
// is a miss, never a stale answer.
func descriptionKey(content []byte, providerID, modelID string) string {
	sum := sha256.New()
	sum.Write(content)
	sum.Write([]byte("\x00" + providerID + "\x00" + modelID + "\x00" + descriptionPromptVersion))
	return hex.EncodeToString(sum.Sum(nil))
}

// describeImage asks the vision model about one image. The call is a plain
// one-message conversation — no tools, no history — and the reply is the
// description; the tool result is where it lands, so it replays like any
// other tool output and never poses as an owner or assistant line.
func describeImage(ctx context.Context, target dservice.VisionTarget, mediaType string, raw []byte, name string) (string, error) {
	conv, err := aggregate.NewConversation("")
	if err != nil {
		return "", err
	}
	attachment, err := valueobject.NewAttachment(dataURI(mediaType, raw), mediaType)
	if err != nil {
		return "", err
	}
	if name := filepath.Base(name); name != "" && name != "." && name != "/" {
		attachment.SetName(name)
	}
	user, err := entity.NewUserMessage(valueobject.UserInput{Text: descriptionPrompt, Attachments: []valueobject.Attachment{attachment}})
	if err != nil {
		return "", err
	}
	conv.Append(user)
	if err := target.Service.Send(ctx, conv, target.Provider, target.Model.ApplyUnknownContextDefaults(), nil); err != nil {
		return "", err
	}
	last := conv.Last()
	if last == nil || strings.TrimSpace(last.Content()) == "" {
		return "", errors.New("the vision model returned no description")
	}
	return strings.TrimSpace(last.Content()), nil
}

// readRest reads at most limit more bytes from f; a payload over the cap is
// reported, never truncated silently.
func readRest(prefix []byte, f io.Reader, limit int64) ([]byte, error) {
	rest, err := io.ReadAll(io.LimitReader(f, limit+1-int64(len(prefix))))
	if err != nil {
		return nil, err
	}
	if int64(len(prefix))+int64(len(rest)) > limit {
		return nil, fmt.Errorf("the payload exceeds the %d MiB limit", limit>>20)
	}
	return append(append([]byte{}, prefix...), rest...), nil
}

// dataURI inlines the bytes the send will carry; the adapters resolve it at
// send time exactly like a stored data: attachment.
func dataURI(mediaType string, data []byte) string {
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data)
}
