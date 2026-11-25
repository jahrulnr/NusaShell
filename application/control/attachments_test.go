package appcontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoconversation "github.com/jahrulnr/nusashell/infrastructure/repository/conversation"
	repoupload "github.com/jahrulnr/nusashell/infrastructure/repository/upload"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
	"github.com/jahrulnr/nusashell/infrastructure/service/wire"
)

// fileWith writes a file of the given size (a repeatable byte pattern) and
// answers its path.
func fileWith(t *testing.T, dir, name string, size int) string {
	t.Helper()
	path := filepath.Join(dir, name)
	data := make([]byte, size)
	for i := range data {
		data[i] = byte('a' + i%26)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func digestOf(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// attachmentsOf reads the files a send handed over.
func attachmentsOf(t *testing.T, channel chan run) []valueobject.Attachment {
	t.Helper()
	select {
	case r := <-channel:
		return r.attachments
	case <-time.After(2 * time.Second):
		t.Fatal("the send never ran")
	}
	return nil
}

// The spec suite: one entry per contract rule. The four cases below need a
// target, a sender workspace, and a real upload store where the form is a copy.
func TestSendSendsAnAbsolutePathToALocalTarget(t *testing.T) {
	app, rooms, turns := setup(t)
	dir := t.TempDir()
	path := fileWith(t, dir, "notes.txt", 32)
	rooms.env = enum.EnvironmentLocal
	ctx := service.WithWorkspaceDir(t.Context(), dir)

	if _, err := app.Send(ctx, "c_self", SendInput{Thread: "c_other", Text: "read this", Attachments: []string{"notes.txt"}}); err != nil {
		t.Fatal(err)
	}
	got := attachmentsOf(t, turns.runs)
	if len(got) != 1 {
		t.Fatalf("attachments = %d, want 1", len(got))
	}
	if got[0].URI() != "file://"+path {
		t.Fatalf("uri = %q, want the absolute path %q", got[0].URI(), "file://"+path)
	}
	if got[0].SHA256() != digestOf(t, path) {
		t.Fatalf("pin = %q, want the file's digest", got[0].SHA256())
	}
	if got[0].Name() != "notes.txt" || got[0].MediaType() != "text/plain" {
		t.Fatalf("attachment = %+v", got[0])
	}
}

// A target whose filesystem this sender cannot address gets the bytes: a path
// there would name a different host's file. An environment it cannot read is
// treated the same way.
func TestSendCopiesForARemoteTarget(t *testing.T) {
	for _, env := range []enum.Environment{enum.EnvironmentDocker, enum.EnvironmentSSH, ""} {
		t.Run(string(env), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "uploads")
			uploads, err := repoupload.New(root)
			if err != nil {
				t.Fatal(err)
			}
			app, rooms, turns := setupWith(t, &fakeQueue{}, uploads)
			dir := t.TempDir()
			fileWith(t, dir, "shot.png", 64)
			rooms.env = env
			ctx := service.WithWorkspaceDir(t.Context(), dir)

			if _, err := app.Send(ctx, "c_self", SendInput{Thread: "c_other", Text: "look", Attachments: []string{"shot.png"}}); err != nil {
				t.Fatal(err)
			}
			got := attachmentsOf(t, turns.runs)
			if len(got) != 1 {
				t.Fatalf("attachments = %d, want 1", len(got))
			}
			if !strings.HasPrefix(got[0].URI(), "file://"+root+string(filepath.Separator)) {
				t.Fatalf("uri = %q, want a copy under the upload store", got[0].URI())
			}
			if got[0].SHA256() == "" || got[0].MediaType() != "image/png" {
				t.Fatalf("copy = %+v, want a pinned image", got[0])
			}
		})
	}
}

// A remote reference is the provider's to fetch: copying it would duplicate
// content NusaShell does not own.
func TestSendPassesARemoteUrlThrough(t *testing.T) {
	app, rooms, turns := setup(t)
	rooms.env = enum.EnvironmentLocal
	const url = "https://example.com/pic.png"
	if _, err := app.Send(t.Context(), "c_self", SendInput{Thread: "c_other", Text: "see", Attachments: []string{url}}); err != nil {
		t.Fatal(err)
	}
	got := attachmentsOf(t, turns.runs)
	if len(got) != 1 || got[0].URI() != url || got[0].MediaType() != "image/png" {
		t.Fatalf("attachment = %+v, want the URL unchanged", got)
	}
	if got[0].SHA256() != "" {
		t.Fatalf("a remote reference must carry no pin: %+v", got[0])
	}
}

// A path that is not a regular file names something the target cannot read as
// content; it is refused with what it actually is.
func TestSendRefusesADirectory(t *testing.T) {
	app, rooms, turns := setup(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "folder.png"), 0o755); err != nil {
		t.Fatal(err)
	}
	rooms.env = enum.EnvironmentLocal
	ctx := service.WithWorkspaceDir(t.Context(), dir)

	_, err := app.Send(ctx, "c_self", SendInput{Thread: "c_other", Text: "x", Attachments: []string{"folder.png"}})
	if code := apperror.As(err).StatusCode(); code != 400 {
		t.Fatalf("directory = %v (status %d), want 400", err, code)
	}
	if !strings.Contains(err.Error(), "a directory") {
		t.Fatalf("refusal = %v, want it to name what the path is", err)
	}
	select {
	case r := <-turns.runs:
		t.Fatalf("a refused attachment still ran a turn: %+v", r)
	default:
	}
}

func TestSendRefusesTooManyAttachments(t *testing.T) {
	app, rooms, turns := setup(t)
	dir := t.TempDir()
	paths := make([]string, 0, 5)
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"} {
		fileWith(t, dir, name, 8)
		paths = append(paths, name)
	}
	rooms.env = enum.EnvironmentLocal
	ctx := service.WithWorkspaceDir(t.Context(), dir)

	_, err := app.Send(ctx, "c_self", SendInput{Thread: "c_other", Text: "x", Attachments: paths})
	if code := apperror.As(err).StatusCode(); code != 400 {
		t.Fatalf("five attachments = %v (status %d), want 400", err, code)
	}
	if !strings.Contains(err.Error(), "at most 4 attachments (5 given)") {
		t.Fatalf("refusal = %v, want the count limit and the actual count", err)
	}
	select {
	case r := <-turns.runs:
		t.Fatalf("a refused send still ran a turn: %+v", r)
	default:
	}
}

// The per-attachment cap follows the wire, so the refusal names the limit that
// was hit and the size that hit it.
func TestSendRefusesOverTheByteCap(t *testing.T) {
	app, rooms, turns := setup(t)
	dir := t.TempDir()
	fileWith(t, dir, "huge.png", 10<<20+1) // one byte past the image cap
	rooms.env = enum.EnvironmentLocal
	ctx := service.WithWorkspaceDir(t.Context(), dir)

	_, err := app.Send(ctx, "c_self", SendInput{Thread: "c_other", Text: "x", Attachments: []string{"huge.png"}})
	if code := apperror.As(err).StatusCode(); code != 413 {
		t.Fatalf("oversized image = %v (status %d), want 413", err, code)
	}
	msg := err.Error()
	if !strings.Contains(msg, "10.0 MiB") || !strings.Contains(msg, "huge.png") {
		t.Fatalf("refusal = %v, want the cap and the file", msg)
	}
	select {
	case r := <-turns.runs:
		t.Fatalf("an oversized send still ran a turn: %+v", r)
	default:
	}
}

// The pin is what makes handing over a path safe: the target re-reads the file
// at its own provider call, and a file that changed is refused there instead of
// served as a different picture.
func TestSendRefusesAChangedFileAtResolve(t *testing.T) {
	app, rooms, turns := setup(t)
	dir := t.TempDir()
	path := fileWith(t, dir, "notes.txt", 32)
	rooms.env = enum.EnvironmentLocal
	ctx := service.WithWorkspaceDir(t.Context(), dir)

	if _, err := app.Send(ctx, "c_self", SendInput{Thread: "c_other", Text: "read", Attachments: []string{"notes.txt"}}); err != nil {
		t.Fatal(err)
	}
	att := attachmentsOf(t, turns.runs)[0]

	// The target resolves it at its provider call — unchanged, it serves.
	root := wire.MediaRoot{Dir: dir, Store: repoworkspace.NewLocal()}
	if _, err := wire.ResolveURI(att, root); err != nil {
		t.Fatalf("unchanged file refused: %v", err)
	}
	// Rewritten since accept: refused, naming the file's content, not a silent
	// different picture.
	if err := os.WriteFile(path, []byte("rewritten after the pin"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := wire.ResolveURI(att, root)
	if code := apperror.As(err).StatusCode(); code != 400 {
		t.Fatalf("changed file = %v (status %d), want 400", err, code)
	}
	if !strings.Contains(err.Error(), "attachment content changed") {
		t.Fatalf("refusal = %v, want the pin mismatch named", err)
	}
	// Gone: also refused, with the reason.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := wire.ResolveURI(att, root); err == nil {
		t.Fatal("a vanished file resolved anyway")
	}
}

// A queued message carries its attachment reference, not bytes: it survives the
// sender's turn ending and the wait in the queue.
func TestQueuedSendKeepsItsAttachment(t *testing.T) {
	q := &fakeQueue{}
	app, rooms, turns := setupWith(t, q, nil)
	dir := t.TempDir()
	path := fileWith(t, dir, "notes.txt", 16)
	rooms.env = enum.EnvironmentLocal
	rooms.mu.Lock()
	rooms.approvals = nil
	rooms.mu.Unlock()
	turns.busy["c_other"] = true
	ctx := service.WithWorkspaceDir(context.Background(), dir)

	out, err := app.Send(ctx, "c_self", SendInput{Thread: "c_other", Text: "later", Attachments: []string{"notes.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if out["delivery"] != "queued" {
		t.Fatalf("delivery = %v, want queued", out["delivery"])
	}
	if len(q.kept) != 1 || len(q.kept[0]) != 1 {
		t.Fatalf("queued attachments = %v, want the one file", q.kept)
	}
	if got := q.kept[0][0]; got.URI() != "file://"+path || got.SHA256() != digestOf(t, path) {
		t.Fatalf("queued attachment = %+v, want the pinned path", got)
	}
	// A relayed prompt never folds into the running turn, attachment or not:
	// it waits for its own turn, where the file and the provenance both land.
}

// A refused send leaves nothing behind: the copies it stored are taken back, so
// a caller told "nothing was written" finds exactly that in the store.
func TestRefusedSendTakesItsUploadCopiesBack(t *testing.T) {
	root := filepath.Join(t.TempDir(), "uploads")
	uploads, err := repoupload.New(root)
	if err != nil {
		t.Fatal(err)
	}
	// A remote target copies the file; a full queue refuses the message. Both
	// the queue and the upload store arrive through the constructor: a wiring
	// gap must fail here instead of passing on a field the test set itself.
	q := &fakeQueue{full: true, depth: 7}
	app, rooms, turns := setupWith(t, q, uploads)
	rooms.env = enum.EnvironmentDocker
	turns.busy["c_other"] = true
	dir := t.TempDir()
	fileWith(t, dir, "shot.png", 64)
	ctx := service.WithWorkspaceDir(context.Background(), dir)

	_, err = app.Send(ctx, "c_self", SendInput{Thread: "c_other", Text: "look", Attachments: []string{"shot.png"}})
	if code := apperror.As(err).StatusCode(); code != 429 {
		t.Fatalf("full queue = %v (status %d), want 429", err, code)
	}
	entries, rerr := os.ReadDir(root)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(entries) != 0 {
		t.Fatalf("a refused send left %d upload entries: %v", len(entries), entries)
	}
}

// The copies a successful send stored are exactly the ones it attached —
// nothing extra, nothing missing. The store records the ids it minted, so the
// test reads them from the port instead of guessing them from a URI: a change
// to the stored form must not break a correct behaviour.
func TestSuccessfulSendKeepsExactlyItsCopies(t *testing.T) {
	uploads := &recordingUploads{}
	app, rooms, turns := setupWith(t, &fakeQueue{}, uploads)
	rooms.env = enum.EnvironmentDocker
	dir := t.TempDir()
	fileWith(t, dir, "one.png", 32)
	fileWith(t, dir, "two.png", 48)
	ctx := service.WithWorkspaceDir(t.Context(), dir)

	if _, err := app.Send(ctx, "c_self", SendInput{Thread: "c_other", Text: "look", Attachments: []string{"one.png", "two.png"}}); err != nil {
		t.Fatal(err)
	}
	got := attachmentsOf(t, turns.runs)
	if len(got) != 2 {
		t.Fatalf("attachments = %d, want 2", len(got))
	}
	ids := uploads.createdIDs()
	if len(ids) != 2 {
		t.Fatalf("the store minted %d ids, want one per attachment: %v", len(ids), ids)
	}
	for i, att := range got {
		// What the turn carries is the store's own attachment, unchanged.
		if want := uploads.saved(ids[i]); att.URI() != want.URI() || att.SHA256() != want.SHA256() {
			t.Fatalf("attachment %d = %+v, want the stored %+v", i, att, want)
		}
	}
}

// recordingUploads stands in for the upload store and remembers what it minted,
// so a test can name its ids without parsing a URI.
type recordingUploads struct {
	drepo.Uploads
	mu        sync.Mutex
	next      int
	ids       []string
	removed   []string
	saveCalls int
	stored    map[string]valueobject.Attachment
}

// saves reports how many copies the store was asked to keep, over the test's
// whole life — a copy a refused send took back still counts as made once.
func (u *recordingUploads) saves() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.saveCalls
}

func (u *recordingUploads) Save(_ context.Context, name, mediaType string, data []byte) (string, valueobject.Attachment, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.stored == nil {
		u.stored = map[string]valueobject.Attachment{}
	}
	u.next++
	id := fmt.Sprintf("u_test_upload_%d", u.next)
	att, err := valueobject.NewAttachment("file:///uploads/"+id+"/"+name, mediaType)
	if err != nil {
		return "", valueobject.Attachment{}, err
	}
	att.SetName(name)
	sum := sha256.Sum256(data)
	att.SetSHA256(hex.EncodeToString(sum[:]))
	u.ids = append(u.ids, id)
	u.stored[id] = att
	u.saveCalls++
	return id, att, nil
}

func (u *recordingUploads) deleted() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.removed...)
}

func (u *recordingUploads) Delete(_ context.Context, id string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.removed = append(u.removed, id)
	delete(u.stored, id)
	kept := u.ids[:0]
	for _, existing := range u.ids {
		if existing != id {
			kept = append(kept, existing)
		}
	}
	u.ids = kept
	return nil
}

func (u *recordingUploads) createdIDs() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.ids...)
}

func (u *recordingUploads) saved(id string) valueobject.Attachment {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.stored[id]
}

// New wires both the queue and the upload store it is given: the name of the
// test is two claims, and each has to be exercised through the constructor.
// A message with an attachment to a remote target is the one path that needs
// both — it copies into the store and then hands the message to the queue —
// so a parameter dropped from New fails here instead of passing on a field the
// test set itself.
func TestNewWiresTheQueueAndTheUploadStore(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		uploads := &recordingUploads{}
		q := &fakeQueue{}
		app, rooms, turns := setupWith(t, q, uploads)
		rooms.env = enum.EnvironmentDocker
		turns.busy["c_other"] = true
		dir := t.TempDir()
		fileWith(t, dir, "shot.png", 32)
		ctx := service.WithWorkspaceDir(t.Context(), dir)

		out, err := app.Send(ctx, "c_self", SendInput{Thread: "c_other", Text: "look", Attachments: []string{"shot.png"}})
		if err != nil {
			t.Fatalf("send through the wired queue and store = %v", err)
		}
		if out["delivery"] != "queued" {
			t.Fatalf("delivery = %v, want queued", out["delivery"])
		}
		if len(q.enqueued) != 1 {
			t.Fatalf("the wired queue saw %d messages, want 1", len(q.enqueued))
		}
		if got := uploads.saves(); got != 1 {
			t.Fatalf("the wired store made %d copies, want 1", got)
		}
		if ids := uploads.createdIDs(); len(ids) != 1 {
			t.Fatalf("the store holds %v, want the one copy", ids)
		}
		if len(uploads.deleted()) != 0 {
			t.Fatalf("an accepted send took its copy back: %v", uploads.deleted())
		}
	})

	t.Run("refused", func(t *testing.T) {
		uploads := &recordingUploads{}
		q := &fakeQueue{full: true, depth: 7}
		app, rooms, turns := setupWith(t, q, uploads)
		rooms.env = enum.EnvironmentDocker
		turns.busy["c_other"] = true
		dir := t.TempDir()
		fileWith(t, dir, "shot.png", 32)
		ctx := service.WithWorkspaceDir(t.Context(), dir)

		_, err := app.Send(ctx, "c_self", SendInput{Thread: "c_other", Text: "look", Attachments: []string{"shot.png"}})
		if code := apperror.As(err).StatusCode(); code != 429 {
			t.Fatalf("full queue = %v (status %d), want 429", err, code)
		}
		if got := uploads.saves(); got != 1 {
			t.Fatalf("the wired store made %d copies, want the one it then took back", got)
		}
		if got := uploads.deleted(); len(got) != 1 {
			t.Fatalf("the refused send took back %v, want exactly its copy", got)
		}
		if ids := uploads.createdIDs(); len(ids) != 0 {
			t.Fatalf("the store still holds %v after the refusal", ids)
		}
	})
}

// Every answer a caller renders as a row names the thread it touched: Send and
// Stop carry the same thread object Read and Wait do, so a transcript line can
// print "@crow Builder" instead of an id. The raw threadId stays for the
// callers that already append to it.
func TestSendAndStopCarryTheThread(t *testing.T) {
	app, rooms, turns := setup(t)
	ctx := context.Background()

	out, err := app.Send(ctx, "c_self", SendInput{Thread: "c_other", Text: "ship it"})
	if err != nil {
		t.Fatal(err)
	}
	assertThreadField(t, out, "c_other", "crow", "Builder", false)

	// A busy room's two other outcomes carry it too.
	turns.busy["c_other"] = true
	out, err = app.Send(ctx, "c_self", SendInput{Thread: "c_other", Text: "also"})
	if err != nil || out["delivery"] != "queued" {
		t.Fatalf("queued = %v, %v", out, err)
	}
	assertThreadField(t, out, "c_other", "crow", "Builder", false)

	stop, err := app.Stop(ctx, "c_self", StopInput{Thread: "c_other"})
	if err != nil {
		t.Fatal(err)
	}
	assertThreadField(t, stop, "c_other", "crow", "Builder", false)

	// A room with no label says so: the field is not invented from the title.
	rooms.mu.Lock()
	rooms.infos = append(rooms.infos, drepo.ConversationInfo{ID: "c_plain", Title: "Unlabelled room", ProviderID: "prov", ModelID: "m1"})
	rooms.mu.Unlock()
	stop, err = app.Stop(ctx, "c_self", StopInput{Thread: "c_plain"})
	if err != nil {
		t.Fatal(err)
	}
	assertThreadField(t, stop, "c_plain", "", "Unlabelled room", false)
}

// Search hits carry the same label, so a search row and a thread row are named
// alike — and a hit in an unlabelled room keeps the label empty.
func TestSearchHitsCarryTheLabel(t *testing.T) {
	app, rooms, _ := setup(t)
	rooms.mu.Lock()
	rooms.infos = append(rooms.infos, drepo.ConversationInfo{ID: "c_plain", Title: "Unlabelled room", ProviderID: "prov", ModelID: "m1"})
	rooms.mu.Unlock()
	rooms.add("c_plain", enum.RoleUser, "plain text here")

	out, err := app.Search(context.Background(), "c_self", SearchInput{Query: "built"})
	if err != nil {
		t.Fatal(err)
	}
	hits := out["hits"].([]Hit)
	if len(hits) != 1 || hits[0].Label != "crow" {
		t.Fatalf("labelled hit = %+v, want label crow", hits)
	}

	out, err = app.Search(context.Background(), "c_self", SearchInput{Query: "plain text"})
	if err != nil {
		t.Fatal(err)
	}
	hits = out["hits"].([]Hit)
	if len(hits) != 1 || hits[0].Label != "" {
		t.Fatalf("unlabelled hit = %+v, want an empty label", hits)
	}
}

// assertThreadField checks the thread object an answer carries.
func assertThreadField(t *testing.T, out map[string]any, id, label, title string, self bool) {
	t.Helper()
	thread, ok := out["thread"].(Thread)
	if !ok {
		t.Fatalf("answer has no thread object: %v", out)
	}
	if thread.ID != id || thread.Label != label || thread.Title != title || thread.IsSelf != self {
		t.Fatalf("thread = %+v, want id %q label %q title %q", thread, id, label, title)
	}
}

// The row that starts a thread names it too: Spawn answers with the same thread
// object a send does, titled by the request and unlabelled until the store
// assigns a label — nothing invented.
func TestSpawnCarriesTheNewThread(t *testing.T) {
	app, _, turns := setup(t)
	ctx := context.Background()

	out, err := app.Spawn(ctx, "c_self", SpawnInput{Prompt: "research", Title: "Research"})
	if err != nil {
		t.Fatal(err)
	}
	if out["afterSeq"] != -1 || out["threadId"] == "" {
		t.Fatalf("spawn = %v, want the old fields kept", out)
	}
	thread, ok := out["thread"].(Thread)
	if !ok {
		t.Fatalf("spawn carries no thread object: %v", out)
	}
	if thread.ID != out["threadId"] || thread.Title != "Research" || thread.Label != "" {
		t.Fatalf("thread = %+v, want the new id, the requested title, no label", thread)
	}
	<-turns.runs // the spawn start its turn in the background

	// No title asked for: it stays empty rather than echoing the prompt.
	out, err = app.Spawn(ctx, "c_self", SpawnInput{Prompt: "untitled"})
	if err != nil {
		t.Fatal(err)
	}
	if thread := out["thread"].(Thread); thread.Title != "" {
		t.Fatalf("thread = %+v, want no invented title", thread)
	}
	<-turns.runs
}

// An export of one room names it; an export of a project spans rooms and keeps
// only the counts.
func TestExportCarriesTheThreadItWasAskedAbout(t *testing.T) {
	app, _, _ := setup(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "c_self"), 0o700); err != nil {
		t.Fatal(err)
	}
	outputs, err := repoconversation.NewToolOutputRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	app.outputs = outputs
	ctx := context.Background()

	out, err := app.Export(ctx, "c_self", ExportInput{Thread: "c_other"})
	if err != nil {
		t.Fatal(err)
	}
	thread, ok := out["thread"].(Thread)
	if !ok || thread.ID != "c_other" || thread.Label != "crow" || thread.Title != "Builder" {
		t.Fatalf("export thread = %v, want the room's identity", out["thread"])
	}
	if out["threads"] != 1 {
		t.Fatalf("threads = %v, want 1", out["threads"])
	}

	project, err := app.Export(ctx, "c_self", ExportInput{Project: "Site"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := portThreadKey(project); ok {
		t.Fatalf("a project export carried a thread identity: %v", project)
	}
}

// portThreadKey reports the thread key of an export answer, without assuming
// the value's type.
func portThreadKey(out map[string]any) (any, bool) {
	thread, ok := out["thread"]
	return thread, ok
}

// A search hit always carries both naming fields in its flat shape — label
// present and empty for an unlabelled room — so a hit row never falls back to
// an id.
func TestSearchHitsAlwaysCarryLabelAndTitle(t *testing.T) {
	app, rooms, _ := setup(t)
	rooms.mu.Lock()
	rooms.infos = append(rooms.infos, drepo.ConversationInfo{ID: "c_plain", Title: "Unlabelled room", ProviderID: "prov", ModelID: "m1"})
	rooms.mu.Unlock()
	rooms.add("c_plain", enum.RoleUser, "plain text here")

	out, err := app.Search(context.Background(), "c_self", SearchInput{Query: "plain text"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(out["hits"])
	if err != nil {
		t.Fatal(err)
	}
	var hits []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %s", raw)
	}
	label, hasLabel := hits[0]["label"]
	title, hasTitle := hits[0]["title"]
	if !hasLabel || !hasTitle {
		t.Fatalf("hit = %s, want label and title present", raw)
	}
	if string(label) != `""` || string(title) != `"Unlabelled room"` {
		t.Fatalf("hit names = label %s title %s", label, title)
	}
}
