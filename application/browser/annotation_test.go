package appbrowser

import (
	"context"
	"strings"
	"testing"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repopick "github.com/jahrulnr/nusashell/infrastructure/repository/pick"
	repoupload "github.com/jahrulnr/nusashell/infrastructure/repository/upload"
)

var pngBytes = []byte("\x89PNG\r\n\x1a\nfake")

func annotationStores(t *testing.T) (*App, drepo.Uploads, drepo.Picks) {
	t.Helper()
	uploads, err := repoupload.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	picks, err := repopick.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := New(nil, nil, nil, nil)
	app.UseAnnotations(uploads, picks)
	return app, uploads, picks
}

// A pick resolves only in its own thread; the descriptor rides with its
// screenshot, and a missing upload degrades to a note.
func TestAnnotation(t *testing.T) {
	ctx := context.Background()
	app, uploads, picks := annotationStores(t)
	upload, _, err := uploads.Save(ctx, "element-1.png", "image/png", pngBytes)
	if err != nil {
		t.Fatal(err)
	}
	pick, err := picks.Save(ctx, valueobject.Pick{ConversationID: "c_1", Marker: 1, Upload: upload,
		Descriptor: valueobject.AnnotationDescriptor{TagName: "button", Name: "Sign in"}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := app.Annotation(ctx, "c_2", pick.ID); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("foreign pick err = %v, want 404", err)
	}
	if _, err := app.Annotation(ctx, "c_1", "pk_nope"); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("unknown pick err = %v, want 404", err)
	}

	res, err := app.Annotation(ctx, "c_1", pick.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "Sign in") {
		t.Fatalf("descriptor text = %q", res.Text)
	}
	if string(res.Image) != string(pngBytes) || res.ImageType != "image/png" {
		t.Fatalf("image = %v %q", len(res.Image), res.ImageType)
	}

	// A pick whose upload is gone still resolves, noted.
	lost, err := picks.Save(ctx, valueobject.Pick{ConversationID: "c_1", Marker: 2, Upload: "u_gone",
		Descriptor: valueobject.AnnotationDescriptor{TagName: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	res, err = app.Annotation(ctx, "c_1", lost.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Image) != 0 || !strings.Contains(res.Text, "screenshot unavailable") {
		t.Fatalf("missing upload = %+v", res)
	}
}

// annotateSession returns a fixed descriptor and PNG for every mark.
type annotateSession struct {
	dservice.BrowserSession
}

func (annotateSession) Profile() string { return "default" }

func (annotateSession) Annotate(context.Context, dservice.BrowserMark) (valueobject.AnnotationDescriptor, []byte, error) {
	return valueobject.AnnotationDescriptor{TagName: "button", Name: "Sign in"}, pngBytes, nil
}

type annotateBrowser struct {
	dservice.Browser
	session dservice.BrowserSession
}

func (b *annotateBrowser) Session(string) (dservice.BrowserSession, bool) {
	return b.session, b.session != nil
}

// recWriter records what Append sees, one event per call.
type recWriter struct {
	drepo.RecordingWriter
	events []recEvent
}

type recEvent struct {
	origin string
	typ    string
	data   any
}

func (w *recWriter) Append(origin, typ string, data any) {
	w.events = append(w.events, recEvent{origin, typ, data})
}

// A mark made while the thread records lands on the timeline by pick id.
func TestAnnotateNotesTheLiveRecordingByPick(t *testing.T) {
	ctx := context.Background()
	app, _, _ := annotationStores(t)
	app.browser = &annotateBrowser{session: annotateSession{}}
	w := &recWriter{}
	app.live["c_1"] = w

	pick, err := app.Annotate(ctx, "c_1", AnnotateInput{Kind: "element", X: 10, Y: 20, Marker: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(w.events) != 1 {
		t.Fatalf("events = %+v", w.events)
	}
	ev := w.events[0]
	if ev.origin != "dom" || ev.typ != "annotation" {
		t.Fatalf("event = %+v", ev)
	}
	data, ok := ev.data.(map[string]any)
	if !ok || data["pickId"] != pick.PickID {
		t.Fatalf("pickId = %+v, want %q", ev.data, pick.PickID)
	}
	if data["marker"] != pick.Marker || data["kind"] != "element" {
		t.Fatalf("event data = %+v", data)
	}
}
