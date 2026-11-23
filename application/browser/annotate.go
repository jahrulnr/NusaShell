package appbrowser

import (
	"context"
	"strconv"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// UseAnnotations sets where marks made on the live page are kept: the
// screenshot as an upload, the mark as a pick a message can carry.
func (a *App) UseAnnotations(uploads drepo.Uploads, picks drepo.Picks) {
	a.uploads, a.picks = uploads, picks
}

// AnnotateInput is one mark the owner makes in the live view.
type AnnotateInput struct {
	Kind   string  `json:"kind"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Marker int     `json:"marker"`
}

// AnnotationPick is a mark ready for the composer.
type AnnotationPick struct {
	PickID     string                           `json:"pickId"`
	Marker     int                              `json:"marker"`
	Descriptor valueobject.AnnotationDescriptor `json:"descriptor"`
	Upload     string                           `json:"upload"`
}

// running is the thread's open session; marks never start one.
func (a *App) running(convID string) (dservice.BrowserSession, error) {
	s, ok := a.browser.Session(convID)
	if !ok {
		return nil, apperror.New(409, "the thread's browser is not open", nil)
	}
	return s, nil
}

// Annotate marks the live page and keeps the mark for the composer.
func (a *App) Annotate(ctx context.Context, convID string, in AnnotateInput) (AnnotationPick, error) {
	if a.uploads == nil || a.picks == nil {
		return AnnotationPick{}, apperror.New(503, "annotations unavailable", nil)
	}
	kind := enum.AnnotationElement
	if in.Kind != "" {
		k, ok := enum.ParseAnnotationKind(in.Kind)
		if !ok {
			return AnnotationPick{}, apperror.New(400, "unknown annotation kind "+in.Kind+"; try element, region, or page", nil)
		}
		kind = k
	}
	s, err := a.running(convID)
	if err != nil {
		return AnnotationPick{}, err
	}
	marker := max(in.Marker, 1)
	d, png, err := s.Annotate(ctx, dservice.BrowserMark{Kind: kind, X: in.X, Y: in.Y, Width: in.Width, Height: in.Height, Marker: marker})
	if err != nil {
		return AnnotationPick{}, err
	}
	upload, _, err := a.uploads.Save(ctx, kind.String()+"-"+strconv.Itoa(marker)+".png", "image/png", png)
	if err != nil {
		return AnnotationPick{}, err
	}
	p, err := a.picks.Save(ctx, valueobject.Pick{ConversationID: convID, Marker: marker, Descriptor: d, Upload: upload})
	if err != nil {
		return AnnotationPick{}, err
	}
	// A live recording marks the pick on its timeline — by pick id, so
	// browser_get_annotation resolves the same one.
	a.mu.Lock()
	w := a.live[convID]
	a.mu.Unlock()
	if w != nil {
		w.Append("dom", "annotation", map[string]any{"pickId": p.ID, "marker": p.Marker, "kind": kind.String(), "label": d.Label(), "selector": d.Selector, "rect": d.Rect})
	}
	return AnnotationPick{PickID: p.ID, Marker: p.Marker, Descriptor: p.Descriptor, Upload: p.Upload}, nil
}

// Annotation resolves a mark by its pick id: the descriptor JSON and its
// marked screenshot. A pick made in another thread is a 404 like an
// unknown one; a lost screenshot degrades to a note, not a failure.
func (a *App) Annotation(ctx context.Context, convID, id string) (Result, error) {
	if a.picks == nil {
		return Result{}, apperror.New(503, "annotations unavailable", nil)
	}
	p, err := a.picks.Get(ctx, id)
	if apperror.As(err).StatusCode() == apperror.StatusNotFound || (err == nil && p.ConversationID != convID) {
		return Result{}, apperror.NewNotFoundError("annotation "+id, err)
	}
	if err != nil {
		return Result{}, err
	}
	out := Result{Text: jsonText(p.Descriptor)}
	var data []byte
	var mime string
	if a.uploads != nil {
		data, mime, _ = a.uploads.Read(ctx, p.Upload)
	}
	if data != nil {
		out.Image, out.ImageType = data, mime
	} else {
		out.Text += "\n\nscreenshot unavailable"
	}
	return out, nil
}

// PreviewAnnotate is the element under a hover point.
func (a *App) PreviewAnnotate(ctx context.Context, convID string, x, y float64) (dservice.BrowserMarkPreview, error) {
	s, err := a.running(convID)
	if err != nil {
		return dservice.BrowserMarkPreview{}, err
	}
	return s.PreviewAnnotate(ctx, x, y)
}

// ClearAnnotate removes a badge left on the live page.
func (a *App) ClearAnnotate(ctx context.Context, convID string) {
	if s, ok := a.browser.Session(convID); ok {
		s.ClearAnnotate(ctx)
	}
}
