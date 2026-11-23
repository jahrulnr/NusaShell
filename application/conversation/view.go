package appconversation

import (
	"bytes"
	"context"
	"encoding/json"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// NewThreadView keys the view of the composer for a thread not started
// yet; every other view is keyed by its conversation id.
const NewThreadView = "new"

// maxViewBytes bounds one stored view. A draft that outgrows it is not
// lost in the page, it only stops following the owner to other devices.
const maxViewBytes = 256 << 10

// UseThreadViews sets where the owner's thread views are kept.
func (app *ConversationApp) UseThreadViews(v drepo.ThreadViews) {
	app.mu.Lock()
	app.views = v
	app.mu.Unlock()
}

// View is what the owner left in a thread's view — the WebUI's own
// document — and when it was written (Unix ms; 0 when nothing is kept).
type View struct {
	Document  json.RawMessage `json:"document"`
	UpdatedAt int64           `json:"updatedAt"`
}

func (app *ConversationApp) threadViews() (drepo.ThreadViews, error) {
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.views == nil {
		return nil, apperror.New(503, "thread views are unavailable", nil)
	}
	return app.views, nil
}

func validViewKey(key string) error {
	if key == NewThreadView {
		return nil
	}
	if err := valueobject.ValidID(key); err != nil {
		return apperror.New(400, "invalid conversation id", err)
	}
	return nil
}

// View returns a thread's view; a thread with none answers an empty one.
func (app *ConversationApp) View(ctx context.Context, key string) (View, error) {
	if err := validViewKey(key); err != nil {
		return View{}, err
	}
	views, err := app.threadViews()
	if err != nil {
		return View{}, err
	}
	doc, at, err := views.Get(ctx, key)
	if apperror.As(err).StatusCode() == apperror.StatusNotFound {
		return View{Document: json.RawMessage(`{}`)}, nil
	}
	if err != nil {
		return View{}, err
	}
	return View{Document: doc, UpdatedAt: at.UnixMilli()}, nil
}

// SaveView replaces a thread's view with a JSON object; an empty object
// forgets it. Only a thread that exists keeps one, so a save racing the
// thread's delete leaves nothing behind.
func (app *ConversationApp) SaveView(ctx context.Context, key string, doc json.RawMessage) (View, error) {
	if err := validViewKey(key); err != nil {
		return View{}, err
	}
	views, err := app.threadViews()
	if err != nil {
		return View{}, err
	}
	if len(doc) > maxViewBytes {
		return View{}, apperror.New(413, "the view is larger than 256 KB", nil)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(doc, &fields); err != nil || fields == nil {
		return View{}, apperror.New(400, "a view is a JSON object", err)
	}
	exists := func() error {
		if key == NewThreadView {
			return nil
		}
		_, err := app.depsSnapshot().Convs.Info(ctx, key)
		return err
	}
	if err := exists(); err != nil {
		return View{}, err
	}
	if len(fields) == 0 {
		return View{Document: json.RawMessage(`{}`)}, views.Delete(ctx, key)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, doc); err != nil {
		return View{}, apperror.New(400, "a view is a JSON object", err)
	}
	at, err := views.Put(ctx, key, compact.Bytes())
	if err != nil {
		return View{}, err
	}
	if err := exists(); apperror.As(err).StatusCode() == apperror.StatusNotFound {
		_ = views.Delete(context.WithoutCancel(ctx), key)
		return View{}, err
	}
	return View{Document: compact.Bytes(), UpdatedAt: at.UnixMilli()}, nil
}
