package appconversation

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appjob "github.com/jahrulnr/nusashell/application/job"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repopick "github.com/jahrulnr/nusashell/infrastructure/repository/pick"
	reporecording "github.com/jahrulnr/nusashell/infrastructure/repository/recording"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	repothreadview "github.com/jahrulnr/nusashell/infrastructure/repository/threadview"
	repoupload "github.com/jahrulnr/nusashell/infrastructure/repository/upload"
	"github.com/jahrulnr/nusashell/migration"
)

// Deleting an idle room removes storage and retires its worker.
func TestDeleteIdle(t *testing.T) {
	app, _ := fixture(t, okHandler)
	if _, err := app.Send(t.Context(), "room", "gw1", "m1", "hi", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := app.Delete(t.Context(), "room"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.deps.Convs.Load(t.Context(), "room"); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("room must be gone: %v", err)
	}
	waitWorkers(t, app, 0)
}

// Delete on a nonexistent room is an honest 404 — and the worker spawned
// to answer it evaporates.
func TestDeleteAbsentRoom(t *testing.T) {
	app, _ := fixture(t, okHandler)
	if err := app.Delete(t.Context(), "ghost"); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("delete missing room → %v, want 404", err)
	}
	waitWorkers(t, app, 0)
}

// Delete preempts a live turn: the running send dies with 408, storage
// goes, and no late persistence may resurrect the room.
func TestDeleteWhileLive(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	entered := make(chan struct{})
	var enteredOnce sync.Once
	app, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		enteredOnce.Do(func() { close(entered) })
		<-release
		okHandler(w, r)
	})
	sent := make(chan error, 1)
	go func() {
		_, err := app.Send(t.Context(), "room", "gw1", "m1", "ping", nil, appjob.SendOptions{}, nil)
		sent <- err
	}()
	select {
	case <-entered: // the send is provably holding the slot
	case <-time.After(10 * time.Second):
		t.Fatal("send did not reach the upstream")
	}

	if err := app.Delete(t.Context(), "room"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-sent:
		if apperror.As(err).StatusCode() != 408 {
			t.Fatalf("killed send → %v, want 408", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("delete did not reach the running send")
	}
	if _, err := app.deps.Convs.Load(t.Context(), "room"); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("room must be gone: %v", err)
	}
	waitWorkers(t, app, 0)
}

// A queued send behind the killed one gets 410 — the room it was waiting
// for no longer exists.
func TestDeleteFlushesPending(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	entered := make(chan struct{})
	var enteredOnce sync.Once
	app, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		enteredOnce.Do(func() { close(entered) })
		<-release
		okHandler(w, r)
	})
	go app.Send(t.Context(), "room", "gw1", "m1", "first", nil, appjob.SendOptions{}, nil)
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("first send did not reach the upstream")
	}
	queuedCtx := &signaledContext{Context: t.Context(), observed: make(chan struct{})}
	queued := make(chan error, 1)
	go func() {
		_, err := app.Send(queuedCtx, "room", "", "", "second", nil, appjob.SendOptions{}, nil)
		queued <- err
	}()
	select {
	case <-queuedCtx.observed: // second is queued behind the first
	case <-time.After(10 * time.Second):
		t.Fatal("second send never queued")
	}

	if err := app.Delete(t.Context(), "room"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-queued:
		if apperror.As(err).StatusCode() != 410 {
			t.Fatalf("queued send after delete → %v, want 410", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("queued send not flushed by delete")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("queued send reached upstream after delete: %d calls", got)
	}
	waitWorkers(t, app, 0)
}

// The id is reusable after delete — a Send mints a fresh room with no
// trace of the old history.
func TestSendAfterDeleteMintsFreshRoom(t *testing.T) {
	app, _ := fixture(t, okHandler)
	if _, err := app.Send(t.Context(), "room", "gw1", "m1", "old life", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := app.Delete(t.Context(), "room"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Send(t.Context(), "room", "gw1", "m1", "new life", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	conv, err := app.deps.Convs.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	msgs := conv.Messages()
	if len(msgs) != 3 || msgs[0].Content() != "new life" || msgs[0].Role() != enum.RoleUser {
		t.Fatalf("resurrected room carries old history: %+v", msgs)
	}
}

// Delete is the retirement barrier: when it returns, the room's worker
// must be off the map — not merely marked dead — so the next post spawns
// a fresh worker instead of Submit-ing into a corpse whose drain would
// answer 410 for a room that is already gone.
func TestDeleteRetiresWorkerBeforeReturning(t *testing.T) {
	app, _ := fixture(t, okHandler)
	if _, err := app.Send(t.Context(), "room", "gw1", "m1", "old life", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := app.Delete(t.Context(), "room"); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	_, registered := app.workers["room"]
	app.mu.Unlock()
	if registered {
		t.Fatal("dead worker still registered after Delete returned")
	}
	if _, err := app.Send(t.Context(), "room", "gw1", "m1", "new life", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	conv, err := app.deps.Convs.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	msgs := conv.Messages()
	if len(msgs) != 3 || msgs[0].Content() != "new life" || msgs[0].Role() != enum.RoleUser {
		t.Fatalf("resurrected room carries old history: %+v", msgs)
	}
}

// failDeleteRepo answers 500 for one room's Delete — a survivor whose
// shared artifacts must outlive the purge.
type failDeleteRepo struct {
	drepo.Conversation
	bad string
}

func (r *failDeleteRepo) Delete(ctx context.Context, convID string) error {
	if convID == r.bad {
		return apperror.New(500, "storage refused", nil)
	}
	return r.Conversation.Delete(ctx, convID)
}

// DeleteAll removes every room — the busy one is preempted, not skipped —
// then empties the shared stores. The proof is on disk: the three roots
// are really empty 0700 dirs, and the view table holds zero rows,
// fresh-room key included — not merely a count of zero threads.
func TestDeleteAllPurgesArtifacts(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var gate atomic.Bool
	entered := make(chan struct{})
	var enteredOnce sync.Once
	app, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if gate.Load() {
			enteredOnce.Do(func() { close(entered) })
			<-release
		}
		okHandler(w, r)
	})
	ctx := t.Context()

	uploadRoot := t.TempDir()
	uploads, err := repoupload.New(uploadRoot)
	if err != nil {
		t.Fatal(err)
	}
	app.uploads = uploads
	if _, _, err := uploads.Save(ctx, "note.txt", "text/plain", []byte("x")); err != nil {
		t.Fatal(err)
	}

	pickRoot := t.TempDir()
	picks, err := repopick.New(pickRoot)
	if err != nil {
		t.Fatal(err)
	}
	app.picks = picks
	if _, err := picks.Save(ctx, valueobject.Pick{ConversationID: "r1", Marker: 1}); err != nil {
		t.Fatal(err)
	}

	recRoot := t.TempDir()
	recs := reporecording.NewStore(recRoot, slog.Default())
	t.Cleanup(recs.Shutdown)
	app.UseRecordings(recs)
	if _, err := recs.Start("s1", "r1", "default", "local", func(s string) string { return s }); err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "app.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	views := repothreadview.New(db)
	app.UseThreadViews(views)
	if _, err := views.Put(ctx, "r1", json.RawMessage(`{"pane":"files"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := views.Put(ctx, "", json.RawMessage(`{"draft":true}`)); err != nil {
		t.Fatal(err)
	}

	if _, err := app.Send(ctx, "r1", "gw1", "m1", "hi", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	gate.Store(true)
	sent := make(chan error, 1)
	go func() {
		_, err := app.Send(ctx, "r2", "gw1", "m1", "hold", nil, appjob.SendOptions{}, nil)
		sent <- err
	}()
	select {
	case <-entered: // the busy room is provably holding its upstream
	case <-time.After(10 * time.Second):
		t.Fatal("busy send did not reach the upstream")
	}

	deleted, err := app.DeleteAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}
	select {
	case err := <-sent:
		if apperror.As(err).StatusCode() != 408 {
			t.Fatalf("preempted send → %v, want 408", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("delete-all did not preempt the busy room")
	}

	for name, root := range map[string]string{"uploads": uploadRoot, "picks": pickRoot, "recordings": recRoot} {
		entries, rerr := os.ReadDir(root)
		if rerr != nil || len(entries) > 0 {
			t.Fatalf("%s root not emptied: entries=%d err=%v", name, len(entries), rerr)
		}
		info, serr := os.Stat(root)
		if serr != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s root mode = %v err=%v, want 0700", name, info.Mode().Perm(), serr)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM thread_views`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("thread_views holds %d rows (err=%v)", n, err)
	}
	for _, id := range []string{"r1", "r2"} {
		if _, err := app.deps.Convs.Load(ctx, id); apperror.As(err).StatusCode() != 404 {
			t.Fatalf("%s must be gone: %v", id, err)
		}
	}
	waitWorkers(t, app, 0)
}

// A room that refuses to delete keeps every shared store untouched — the
// survivors' uploads and views may not vanish with the purge, and the
// error names the ids it could not remove.
func TestDeleteAllFailureKeepsArtifacts(t *testing.T) {
	app, _ := fixture(t, okHandler)
	ctx := t.Context()
	if _, err := app.Send(ctx, "r1", "gw1", "m1", "hi", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Send(ctx, "r2", "gw1", "m1", "hi", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	app.deps.Convs = &failDeleteRepo{Conversation: app.deps.Convs, bad: "r2"}

	uploadRoot := t.TempDir()
	uploads, err := repoupload.New(uploadRoot)
	if err != nil {
		t.Fatal(err)
	}
	app.uploads = uploads
	if _, _, err := uploads.Save(ctx, "keep.txt", "text/plain", []byte("x")); err != nil {
		t.Fatal(err)
	}
	views := &memViews{docs: map[string]json.RawMessage{"r2": json.RawMessage(`{"x":1}`), "": json.RawMessage(`{"draft":true}`)}}
	app.UseThreadViews(views)

	deleted, err := app.DeleteAll(ctx)
	if err == nil {
		t.Fatal("expected a partial-failure error")
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1 (r1)", deleted)
	}
	if !strings.Contains(err.Error(), "r2") {
		t.Fatalf("error must name the failed id: %v", err)
	}
	if entries, _ := os.ReadDir(uploadRoot); len(entries) == 0 {
		t.Fatal("uploads purged while r2 survived")
	}
	if !views.has("r2") || !views.has("") {
		t.Fatal("survivor or fresh-room views purged while r2 survived")
	}
	waitWorkers(t, app, 0)
}
