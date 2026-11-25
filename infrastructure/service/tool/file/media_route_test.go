package toolfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/service"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
)

// visionStub is the vision bridge without Settings or a provider repository:
// it answers a scripted reading and resolution and records what was resolved,
// so a refusal can prove nothing was loaded.
type visionStub struct {
	reading  service.VisionReading
	target   service.VisionTarget
	resolved []string
}

func (v *visionStub) ReadSettings(context.Context) (service.VisionReading, error) {
	return v.reading, nil
}

func (v *visionStub) ResolveModel(_ context.Context, providerID, modelID string) (service.VisionTarget, error) {
	v.resolved = append(v.resolved, providerID+"/"+modelID)
	return v.target, nil
}

// visionText is the text adapter a resolved target points at: it records what
// the vision model was shown and answers with a scripted description.
type visionText struct {
	calls int
	seen  []entity.Message
	reply string
}

func (v *visionText) Send(_ context.Context, conv *aggregate.Conversation, _ *aggregate.Provider, _ entity.Model, _ event.TurnEventHandler) error {
	v.calls++
	v.seen = append([]entity.Message(nil), conv.Messages()...)
	answer, err := entity.NewMessage(enum.RoleAssistant, v.reply)
	if err != nil {
		return err
	}
	conv.Append(answer)
	return nil
}

// scriptedTranscriber answers the audio branch: it records the request and
// replies with a scripted transcript.
type scriptedTranscriber struct {
	calls       int
	seen        []service.TranscriptionRequest
	text        string
	err         error
	identity    string
	identityErr error
}

func (s *scriptedTranscriber) Identity(context.Context) (string, error) {
	if s.identityErr != nil {
		return "", s.identityErr
	}
	return s.identity, nil
}

func (s *scriptedTranscriber) Transcribe(_ context.Context, req service.TranscriptionRequest) (string, error) {
	s.calls++
	s.seen = append(s.seen, req)
	if s.err != nil {
		return "", s.err
	}
	return s.text, nil
}

// mp3Fixture is enough of an ID3 header for the sniffer to call it audio.
var mp3Fixture = []byte("ID3\x04\x00\x00\x00\x00\x00\x00")

// memDescriptions is the media-description port over a map, one namespace per
// conversation — the real store's isolation rule without a disk.
type memDescriptions struct {
	rooms map[string]map[string]string
}

func (m *memDescriptions) Get(convID, key string) (string, bool, error) {
	text, ok := m.rooms[convID][key]
	return text, ok, nil
}

func (m *memDescriptions) Put(convID, key, text string) error {
	if m.rooms[convID] == nil {
		m.rooms[convID] = map[string]string{}
	}
	m.rooms[convID][key] = text
	return nil
}

func (m *memDescriptions) Purge(convID string) error { delete(m.rooms, convID); return nil }
func (m *memDescriptions) PurgeAll() error           { clear(m.rooms); return nil }

// mediaHarness wires both file readers over one workspace and a scripted
// vision bridge, so a test can drive the same file through either name.
type mediaHarness struct {
	media       MediaDeps
	vision      *visionStub
	text        *visionText
	transcriber *scriptedTranscriber
	cache       *memDescriptions
	root        string
}

func newMediaHarness(t *testing.T) *mediaHarness {
	t.Helper()
	root := t.TempDir()
	prov, err := aggregate.NewProvider("vision-prov", "stub", "http://unused", enum.StyleCompletion)
	if err != nil {
		t.Fatal(err)
	}
	model, err := entity.NewModel("vision-model")
	if err != nil {
		t.Fatal(err)
	}
	model.SetInputModalities([]enum.Modality{enum.ModalityText, enum.ModalityImage})
	text := &visionText{reply: "A terminal shows the failing test and its stack trace."}
	vision := &visionStub{
		reading: service.VisionReading{ProviderID: "vision-prov", ModelID: "vision-model", Consent: true},
		target:  service.VisionTarget{Provider: &prov, Model: model, Service: text},
	}
	cache := &memDescriptions{rooms: map[string]map[string]string{}}
	transcriber := &scriptedTranscriber{text: "halo, ini rekaman rapat.", identity: "speech-prov/whisper-large"}
	return &mediaHarness{
		media:       MediaDeps{Descriptions: cache, Vision: vision, Transcriber: transcriber},
		vision:      vision,
		text:        text,
		transcriber: transcriber,
		cache:       cache,
		root:        root,
	}
}

func (h *mediaHarness) readMedia(t *testing.T) service.Tool {
	t.Helper()
	tool, err := NewReadMediaTool(repoworkspace.NewLocal(), h.root, h.media)
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func (h *mediaHarness) readFileTool(t *testing.T) service.Tool {
	t.Helper()
	tool, err := NewFileReadTool(repoworkspace.NewLocal(), h.root, h.media)
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func (h *mediaHarness) write(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.root, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// ctx carries the conversation and the turn model's declared modalities —
// the harness's whole answer to "can this model see?".
func (h *mediaHarness) ctx(convID string, mods ...enum.Modality) context.Context {
	ctx := service.WithConversationID(service.WithWorkspaceDir(context.Background(), h.root), convID)
	return service.WithModelModalities(ctx, mods)
}

func runMedia(t *testing.T, tool service.Tool, ctx context.Context, input string) (service.ToolResult, error) {
	t.Helper()
	tc, err := entity.NewToolCall("call_1", tool.Schema().Name(), input, enum.TooltypeOrigin)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Execute(ctx, tc, nil)
}

// A model that declares image input gets the file attached, exactly as
// before the bridge existed — no vision call, no description.
func TestReadMediaAttachesForAVisionModel(t *testing.T) {
	h := newMediaHarness(t)
	h.write(t, "shot.png", pngFixture)

	res, err := runMedia(t, h.readMedia(t), h.ctx("c1", enum.ModalityText, enum.ModalityImage), `{"path":"shot.png"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Attachments) != 1 || res.Attachments[0].Modality() != enum.ModalityImage {
		t.Fatalf("attachments = %+v, want the image", res.Attachments)
	}
	if h.text.calls != 0 {
		t.Fatalf("vision model called %d times for a model that can see", h.text.calls)
	}
	if len(h.vision.resolved) != 0 {
		t.Fatalf("resolved %v for a model that can see", h.vision.resolved)
	}
}

// A model that declares text-only input gets the image described, on the model
// models.vision names, with the file and its name reaching that model.
func TestReadMediaDescribesForABlindModelWithConsent(t *testing.T) {
	h := newMediaHarness(t)
	h.write(t, "shot.png", pngFixture)

	res, err := runMedia(t, h.readMedia(t), h.ctx("c1", enum.ModalityText), `{"path":"shot.png"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != h.text.reply {
		t.Fatalf("result = %q, want the vision model's description", res.Text)
	}
	if len(res.Attachments) != 0 {
		t.Fatalf("a description is text, got %d attachments", len(res.Attachments))
	}
	if h.text.calls != 1 {
		t.Fatalf("vision model calls = %d, want 1", h.text.calls)
	}
	if len(h.vision.resolved) != 1 || h.vision.resolved[0] != "vision-prov/vision-model" {
		t.Fatalf("resolved %v, want the models.vision assignment", h.vision.resolved)
	}
	seen := h.text.seen
	if len(seen) != 1 || seen[0].Content() != descriptionPrompt {
		t.Fatalf("vision call saw %#v, want the describe prompt", seen)
	}
	atts := seen[0].Attachments()
	if len(atts) != 1 || atts[0].Modality() != enum.ModalityImage || atts[0].Name() != "shot.png" {
		t.Fatalf("vision call carried %+v, want the named image", atts)
	}
}

// Consent gates the describe branch: without it the file never leaves, and the
// refusal names the switch and the model that would have read it.
func TestReadMediaRefusesWhenConsentIsOff(t *testing.T) {
	h := newMediaHarness(t)
	h.write(t, "shot.png", pngFixture)
	h.vision.reading.Consent = false

	_, err := runMedia(t, h.readMedia(t), h.ctx("c1", enum.ModalityText), `{"path":"shot.png"}`)
	if err == nil {
		t.Fatal("read_media sent a file without consent")
	}
	for _, want := range []string{"sending files to vision-prov / vision-model is off", "media.reading_consent", "image descriptions and audio transcripts"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not mention %q", err, want)
		}
	}
	if h.text.calls != 0 {
		t.Fatalf("vision model called %d times without consent", h.text.calls)
	}
	if len(h.vision.resolved) != 0 {
		t.Fatalf("resolved %v before consent — the tool must refuse before loading a provider", h.vision.resolved)
	}
}

// A turn whose context carries no modality list keeps the behaviour that
// predates the bridge: attach, and let the encoder degrade the attachment
// afterwards. The fallback is deliberate — a wiring bug must land here, not in
// a silent refusal.
func TestReadMediaFallsBackToAttachingWithoutCapabilityOnContext(t *testing.T) {
	h := newMediaHarness(t)
	h.write(t, "shot.png", pngFixture)

	res, err := runMedia(t, h.readMedia(t), h.ctx("c1"), `{"path":"shot.png"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Attachments) != 1 {
		t.Fatalf("attachments = %d, want the fallback to attach", len(res.Attachments))
	}
	if h.text.calls != 0 || len(h.vision.resolved) != 0 {
		t.Fatalf("the fallback touched the vision bridge: calls=%d resolved=%v", h.text.calls, h.vision.resolved)
	}
}

// The description comes back as text — never as an attachment, so it cannot
// count toward the serving's image cap or appear as a file in the transcript.
func TestDescriptionIsTextAndNeverAnAttachment(t *testing.T) {
	h := newMediaHarness(t)
	h.write(t, "shot.png", pngFixture)

	res, err := runMedia(t, h.readMedia(t), h.ctx("c1", enum.ModalityText), `{"path":"shot.png"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text == "" || len(res.Attachments) != 0 {
		t.Fatalf("result = %+v, want text and no attachments", res)
	}
}

// A description is cached per conversation, keyed by the image and the model:
// the same file is described once in a room, another room never reuses it, and
// another model reads it again.
func TestDescriptionCacheIsPerConversation(t *testing.T) {
	h := newMediaHarness(t)
	h.write(t, "shot.png", pngFixture)
	tool := h.readMedia(t)

	for range 2 {
		res, err := runMedia(t, tool, h.ctx("room-a", enum.ModalityText), `{"path":"shot.png"}`)
		if err != nil {
			t.Fatal(err)
		}
		if res.Text != h.text.reply {
			t.Fatalf("result = %q, want the cached description", res.Text)
		}
	}
	if h.text.calls != 1 {
		t.Fatalf("vision model calls = %d for one image in one room, want 1", h.text.calls)
	}
	if _, err := runMedia(t, tool, h.ctx("room-b", enum.ModalityText), `{"path":"shot.png"}`); err != nil {
		t.Fatal(err)
	}
	if h.text.calls != 2 {
		t.Fatalf("vision model calls = %d after another room, want a fresh send", h.text.calls)
	}
	if _, err := runMedia(t, tool, h.ctx("room-a", enum.ModalityText), `{"path":"shot.png"}`); err != nil {
		t.Fatal(err)
	}
	if h.text.calls != 2 {
		t.Fatalf("vision model calls = %d, want the first room's cache hit", h.text.calls)
	}
}

// The name the encoder's placeholder prints is the tool's own: one constant,
// so the pointer cannot drift from the registration.
func TestMediaReaderNameIsReadMedia(t *testing.T) {
	if ReadMediaToolName != "read_media" {
		t.Fatalf("ReadMediaToolName = %q, want read_media", ReadMediaToolName)
	}
	h := newMediaHarness(t)
	if got := h.readMedia(t).Schema().Name(); got != ReadMediaToolName {
		t.Fatalf("schema name = %q, want %q", got, ReadMediaToolName)
	}
}

// The vision role must name a model that accepts images: describing for a
// blind model with a blind reader would only move the blindness.
func TestVisionModelMustAcceptImages(t *testing.T) {
	h := newMediaHarness(t)
	h.write(t, "shot.png", pngFixture)
	h.vision.target.Model.SetInputModalities([]enum.Modality{enum.ModalityText})

	_, err := runMedia(t, h.readMedia(t), h.ctx("c1", enum.ModalityText), `{"path":"shot.png"}`)
	if err == nil || !strings.Contains(err.Error(), "does not accept images") || !strings.Contains(err.Error(), "vision-model") {
		t.Fatalf("refusal = %v, want the capability gap named", err)
	}
	if h.text.calls != 0 {
		t.Fatalf("vision model called %d times for a model that cannot take images", h.text.calls)
	}
}

// read_file and read_media sniff the same window, so a payload that turns out
// not to be text is not a mistake: read_file routes it exactly as read_media
// would instead of answering with a redirect the model has to act on.
func TestReadFileAttachesNonText(t *testing.T) {
	h := newMediaHarness(t)
	h.write(t, "shot.png", pngFixture)

	res, err := runMedia(t, h.readFileTool(t), h.ctx("c1", enum.ModalityText, enum.ModalityImage), `{"path":"shot.png"}`)
	if err != nil {
		t.Fatalf("read_file refused a non-text payload: %v", err)
	}
	if len(res.Attachments) != 1 || res.Attachments[0].Modality() != enum.ModalityImage {
		t.Fatalf("attachments = %+v, want the image", res.Attachments)
	}
}

// The cross-routing carries the capability decision too: a blind model reading
// a PNG through read_file gets the same description it would get through
// read_media.
func TestReadFileDescribesForABlindModel(t *testing.T) {
	h := newMediaHarness(t)
	h.write(t, "shot.png", pngFixture)

	res, err := runMedia(t, h.readFileTool(t), h.ctx("c1", enum.ModalityText), `{"path":"shot.png"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != h.text.reply || len(res.Attachments) != 0 {
		t.Fatalf("result = %+v, want the description text", res)
	}
}

// And the other direction: read_media on text renders it the way read_file
// does, so neither name can be the wrong choice.
func TestReadMediaReturnsText(t *testing.T) {
	h := newMediaHarness(t)
	h.write(t, "notes.txt", []byte("alpha\nbeta\n"))

	res, err := runMedia(t, h.readMedia(t), h.ctx("c1", enum.ModalityText), `{"path":"notes.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "alpha") || !strings.Contains(res.Text, "beta") || len(res.Attachments) != 0 {
		t.Fatalf("result = %+v, want the text and no attachment", res)
	}
}

// Audio follows the same pattern as images: a model that accepts it gets the
// file attached, and a model that cannot gets the transcript — under the same
// send consent, not a second switch.
func TestReadMediaTranscribesForABlindModelWithConsent(t *testing.T) {
	h := newMediaHarness(t)
	h.write(t, "meeting.mp3", mp3Fixture)

	res, err := runMedia(t, h.readMedia(t), h.ctx("c1", enum.ModalityText), `{"path":"meeting.mp3"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != h.transcriber.text || len(res.Attachments) != 0 {
		t.Fatalf("result = %+v, want the transcript as text", res)
	}
	if h.transcriber.calls != 1 {
		t.Fatalf("transcription calls = %d, want 1", h.transcriber.calls)
	}
	if seen := h.transcriber.seen[0].Audio; seen.MediaType != "audio/mpeg" || len(seen.Data) != len(mp3Fixture) {
		t.Fatalf("transcriber saw %+v, want the audio bytes", seen)
	}
}

// The audio branch sits under the same switch as the image branch: with it off
// the file never leaves, and the refusal names the switch and what it covers.
func TestReadMediaRefusesTranscriptionWithoutConsent(t *testing.T) {
	h := newMediaHarness(t)
	h.write(t, "meeting.mp3", mp3Fixture)
	h.vision.reading.Consent = false

	_, err := runMedia(t, h.readMedia(t), h.ctx("c1", enum.ModalityText), `{"path":"meeting.mp3"}`)
	if err == nil {
		t.Fatal("read_media transcribed audio without consent")
	}
	for _, want := range []string{"sending audio to the transcription model is off", "media.reading_consent", "audio transcripts"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not mention %q", err, want)
		}
	}
	if h.transcriber.calls != 0 {
		t.Fatalf("transcriber called %d times without consent", h.transcriber.calls)
	}
}

// A model that declares audio input gets the audio attached — no transcription,
// no consent needed for a file that never leaves the machine.
func TestReadMediaAttachesAudioForAnAudioCapableModel(t *testing.T) {
	h := newMediaHarness(t)
	h.write(t, "meeting.mp3", mp3Fixture)

	res, err := runMedia(t, h.readMedia(t), h.ctx("c1", enum.ModalityText, enum.ModalityAudio), `{"path":"meeting.mp3"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Attachments) != 1 || res.Attachments[0].Modality() != enum.ModalityAudio {
		t.Fatalf("attachments = %+v, want the audio", res.Attachments)
	}
	if h.transcriber.calls != 0 {
		t.Fatalf("transcriber called %d times for a model that can hear", h.transcriber.calls)
	}
}

// A transcript belongs to the model that produced it: switching the
// transcription role is a cache miss, never the previous reader's words.
func TestTranscriptionCacheIsPerModel(t *testing.T) {
	h := newMediaHarness(t)
	h.write(t, "meeting.mp3", mp3Fixture)
	tool := h.readMedia(t)

	for range 2 {
		if _, err := runMedia(t, tool, h.ctx("room-a", enum.ModalityText), `{"path":"meeting.mp3"}`); err != nil {
			t.Fatal(err)
		}
	}
	if h.transcriber.calls != 1 {
		t.Fatalf("transcription calls = %d for one file in one room, want 1", h.transcriber.calls)
	}

	h.transcriber.identity = "speech-prov/whisper-small"
	if _, err := runMedia(t, tool, h.ctx("room-a", enum.ModalityText), `{"path":"meeting.mp3"}`); err != nil {
		t.Fatal(err)
	}
	if h.transcriber.calls != 2 {
		t.Fatalf("transcription calls = %d after switching the model, want a fresh send", h.transcriber.calls)
	}
}

// An unnamed reader is cached under an explicit "unknown" marker: the key never
// silently omits the identity, a known identity that appears later is a
// different reader (fresh send), and an unreadable role is the same marker
// rather than a crash or a silently different key.
func TestTranscriptionCacheKeepsAnUnknownIdentityExplicit(t *testing.T) {
	h := newMediaHarness(t)
	h.write(t, "meeting.mp3", mp3Fixture)
	tool := h.readMedia(t)
	run := func() {
		t.Helper()
		if _, err := runMedia(t, tool, h.ctx("room-a", enum.ModalityText), `{"path":"meeting.mp3"}`); err != nil {
			t.Fatal(err)
		}
	}

	h.transcriber.identity = ""
	run()
	run()
	if h.transcriber.calls != 1 {
		t.Fatalf("transcription calls = %d under one unknown reader, want the cache hit", h.transcriber.calls)
	}

	// A reader that names itself is another identity: the unknown entry must
	// not answer for it.
	h.transcriber.identity = "speech-prov/whisper-large"
	run()
	if h.transcriber.calls != 2 {
		t.Fatalf("transcription calls = %d after the identity appeared, want a fresh send", h.transcriber.calls)
	}

	// Back to the unknown reader — the first entry, not a third call.
	h.transcriber.identity = ""
	run()
	if h.transcriber.calls != 2 {
		t.Fatalf("transcription calls = %d back under the unknown reader, want the cache hit", h.transcriber.calls)
	}

	// An unreadable role is the same explicit marker, not a silent third key.
	h.transcriber.identityErr = errors.New("settings unreadable")
	run()
	if h.transcriber.calls != 2 {
		t.Fatalf("transcription calls = %d with an unreadable identity, want the unknown entry", h.transcriber.calls)
	}

	// The marker cannot be reached by a real identity: a reader literally
	// named "unknown" is another reader, not the sentinel.
	h.transcriber.identityErr = nil
	h.transcriber.identity = "unknown"
	run()
	if h.transcriber.calls != 3 {
		t.Fatalf("transcription calls = %d for a reader named %q, want a fresh send", h.transcriber.calls, "unknown")
	}
}

// B-023: a voice note must not be refused as an unreadable "file". Both doors
// sniff through one rule, so a .m4a the transcription role can read routes as
// audio — an attachment for a model that takes audio, a transcript otherwise.
func TestReadRoutesM4aAsAudio(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "test", "media", "audio.m4a"))
	if err != nil {
		t.Skip("fixture missing:", err)
	}
	h := newMediaHarness(t)
	h.write(t, "voice.m4a", raw)

	// A model that takes audio gets the file attached, named as audio.
	res, err := runMedia(t, h.readMedia(t), h.ctx("c1", enum.ModalityText, enum.ModalityAudio), `{"path":"voice.m4a"}`)
	if err != nil {
		t.Fatalf("read_media refused an m4a voice note: %v", err)
	}
	if len(res.Attachments) != 1 || res.Attachments[0].Modality() != enum.ModalityAudio || res.Attachments[0].MediaType() != "audio/mp4" {
		t.Fatalf("attachments = %+v, want the audio/mp4 voice note", res.Attachments)
	}

	// A model that cannot hear it reads it through the transcription role.
	res, err = runMedia(t, h.readMedia(t), h.ctx("c2", enum.ModalityText), `{"path":"voice.m4a"}`)
	if err != nil {
		t.Fatalf("read_media refused to transcribe an m4a: %v", err)
	}
	if res.Text != h.transcriber.text || h.transcriber.calls != 1 {
		t.Fatalf("result = %q calls = %d, want the transcript", res.Text, h.transcriber.calls)
	}
	if seen := h.transcriber.seen[0].Audio; seen.MediaType != "audio/mp4" {
		t.Fatalf("transcriber saw %q, want audio/mp4", seen.MediaType)
	}

	// And through read_file, the door QA used.
	res, err = runMedia(t, h.readFileTool(t), h.ctx("c3", enum.ModalityText, enum.ModalityAudio), `{"path":"voice.m4a"}`)
	if err != nil {
		t.Fatalf("read_file refused an m4a voice note: %v", err)
	}
	if len(res.Attachments) != 1 || res.Attachments[0].Modality() != enum.ModalityAudio {
		t.Fatalf("read_file attachments = %+v, want the audio", res.Attachments)
	}

	// The exact QA reproduction: read_file on a blind model with the
	// transcription role set — a transcript, never "nothing can hand file".
	res, err = runMedia(t, h.readFileTool(t), h.ctx("c4", enum.ModalityText), `{"path":"voice.m4a"}`)
	if err != nil {
		t.Fatalf("read_file refused to transcribe an m4a: %v", err)
	}
	if res.Text != h.transcriber.text || h.transcriber.calls != 2 {
		t.Fatalf("result = %q calls = %d, want the transcript", res.Text, h.transcriber.calls)
	}
}

// An .mp4 stays video: the extension rule fixes only the audio-only MPEG-4
// container, never the video one.
func TestReadKeepsMp4AsVideo(t *testing.T) {
	h := newMediaHarness(t)
	h.write(t, "clip.mp4", []byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"))

	res, err := runMedia(t, h.readMedia(t), h.ctx("c1", enum.ModalityText, enum.ModalityVideo), `{"path":"clip.mp4"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Attachments) != 1 || res.Attachments[0].Modality() != enum.ModalityVideo || res.Attachments[0].MediaType() != "video/mp4" {
		t.Fatalf("attachments = %+v, want the video", res.Attachments)
	}

	// A blind model is told video — not mistaken for audio and transcribed.
	_, err = runMedia(t, h.readMedia(t), h.ctx("c2", enum.ModalityText), `{"path":"clip.mp4"}`)
	if err == nil || !strings.Contains(err.Error(), "video") {
		t.Fatalf("refusal = %v, want the video modality named", err)
	}
	if h.transcriber.calls != 0 {
		t.Fatalf("transcriber called %d times for a video", h.transcriber.calls)
	}
}

// An Ogg stream is audio unless its name says .ogv — the one place the
// extension, not the bytes, tells the two apart.
func TestReadTellsOggAudioFromOggVideo(t *testing.T) {
	ogg := []byte("OggS\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00")
	h := newMediaHarness(t)
	h.write(t, "voice.ogg", ogg)
	h.write(t, "clip.ogv", ogg)

	res, err := runMedia(t, h.readMedia(t), h.ctx("c1", enum.ModalityText, enum.ModalityAudio), `{"path":"voice.ogg"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Attachments) != 1 || res.Attachments[0].MediaType() != "audio/ogg" {
		t.Fatalf("attachments = %+v, want audio/ogg", res.Attachments)
	}
	res, err = runMedia(t, h.readMedia(t), h.ctx("c2", enum.ModalityText, enum.ModalityVideo), `{"path":"clip.ogv"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Attachments) != 1 || res.Attachments[0].MediaType() != "video/ogg" {
		t.Fatalf("attachments = %+v, want video/ogg", res.Attachments)
	}
}

// The containers the byte sniffer misses (aac, flac) route as audio too, so a
// blind model transcribes them instead of meeting "nothing can hand file".
func TestReadRoutesAacAndFlacAsAudio(t *testing.T) {
	h := newMediaHarness(t)
	h.write(t, "voice.aac", []byte{0xFF, 0xF1, 0x50, 0x80, 0x00, 0x1F, 0xFC})
	h.write(t, "voice.flac", []byte("fLaC\x00\x00\x00\x22"))

	for _, room := range []string{"room-aac", "room-flac"} {
		name := "voice.aac"
		if room == "room-flac" {
			name = "voice.flac"
		}
		if _, err := runMedia(t, h.readMedia(t), h.ctx(room, enum.ModalityText), `{"path":"`+name+`"}`); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if h.transcriber.calls != 2 {
		t.Fatalf("transcription calls = %d, want one per container", h.transcriber.calls)
	}
	if got := h.transcriber.seen[0].Audio.MediaType; got != "audio/aac" {
		t.Fatalf("aac media type = %q", got)
	}
	if got := h.transcriber.seen[1].Audio.MediaType; got != "audio/flac" {
		t.Fatalf("flac media type = %q", got)
	}
}

// Text keeps its own path through both doors: the shared sniffer must not make
// a document look like media.
func TestReadKeepsTextOnTheTextPath(t *testing.T) {
	h := newMediaHarness(t)
	h.write(t, "notes.txt", []byte("alpha\nbeta\n"))

	res, err := runMedia(t, h.readFileTool(t), h.ctx("c1", enum.ModalityText), `{"path":"notes.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "alpha") || len(res.Attachments) != 0 {
		t.Fatalf("read_file = %+v, want the text and no attachment", res)
	}
}
