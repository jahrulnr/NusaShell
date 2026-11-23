package appconversation

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	appjob "github.com/jahrulnr/nusashell/application/job"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

type memViews struct {
	mu   sync.Mutex
	docs map[string]json.RawMessage
}

func (m *memViews) Get(_ context.Context, key string) (json.RawMessage, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.docs[key]
	if !ok {
		return nil, time.Time{}, apperror.NewNotFoundError("view", nil)
	}
	return doc, time.UnixMilli(5), nil
}

func (m *memViews) Put(_ context.Context, key string, doc json.RawMessage) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.docs[key] = doc
	return time.UnixMilli(5), nil
}

func (m *memViews) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.docs, key)
	return nil
}

func (m *memViews) Purge(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.docs = map[string]json.RawMessage{}
	return nil
}

func (m *memViews) has(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.docs[key]
	return ok
}

// A thread's view is kept while the thread lives and goes with it; the
// new-thread composer has one of its own.
func TestThreadViewFollowsItsThread(t *testing.T) {
	app, _ := fixture(t, okHandler)
	views := &memViews{docs: map[string]json.RawMessage{}}
	app.UseThreadViews(views)
	ctx := t.Context()

	if v, err := app.View(ctx, "room"); err != nil || string(v.Document) != "{}" || v.UpdatedAt != 0 {
		t.Fatalf("nothing kept reads as an empty view: %+v %v", v, err)
	}
	if _, err := app.SaveView(ctx, "room", json.RawMessage(`{"draft":"x"}`)); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("a thread that does not exist keeps no view: %v", err)
	}
	if _, err := app.Send(ctx, "room", "gw1", "m1", "hi", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	v, err := app.SaveView(ctx, "room", json.RawMessage(`{ "draft": "half", "pane": "files" }`))
	if err != nil || string(v.Document) != `{"draft":"half","pane":"files"}` || v.UpdatedAt != 5 {
		t.Fatalf("saved: %+v %v", v, err)
	}
	if got, _ := app.View(ctx, "room"); string(got.Document) != `{"draft":"half","pane":"files"}` {
		t.Fatalf("read back: %s", got.Document)
	}
	if _, err := app.SaveView(ctx, NewThreadView, json.RawMessage(`{"draft":"next"}`)); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{`[1]`, `null`, `"x"`, `{`} {
		if _, err := app.SaveView(ctx, "room", json.RawMessage(bad)); apperror.As(err).StatusCode() != 400 {
			t.Fatalf("%s must be refused: %v", bad, err)
		}
	}
	big := `{"draft":"` + string(make([]byte, maxViewBytes)) + `"}`
	if _, err := app.SaveView(ctx, "room", json.RawMessage(big)); apperror.As(err).StatusCode() != 413 {
		t.Fatalf("an oversized view: %v", err)
	}
	if _, err := app.View(ctx, "../x"); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("a path is not a key: %v", err)
	}

	if _, err := app.SaveView(ctx, "room", json.RawMessage(`{}`)); err != nil || views.has("room") {
		t.Fatalf("an empty view forgets the kept one: %v", err)
	}
	if _, err := app.SaveView(ctx, "room", json.RawMessage(`{"draft":"again"}`)); err != nil {
		t.Fatal(err)
	}
	if err := app.Delete(ctx, "room"); err != nil {
		t.Fatal(err)
	}
	if views.has("room") || !views.has(NewThreadView) {
		t.Fatal("deleting the thread drops its view and only its view")
	}
}
