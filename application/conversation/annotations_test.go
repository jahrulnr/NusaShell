package appconversation

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repopick "github.com/jahrulnr/nusashell/infrastructure/repository/pick"
	repoupload "github.com/jahrulnr/nusashell/infrastructure/repository/upload"
)

// A pick resolves only in its own thread; its screenshot rides only when
// the owner kept it, and the annotation reaches history and the model.
func TestSendCarriesAnnotations(t *testing.T) {
	var mu sync.Mutex
	var body string
	app, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = string(raw)
		mu.Unlock()
		okHandler(w, r)
	})
	uploads, _ := repoupload.New(t.TempDir())
	picks, _ := repopick.New(t.TempDir())
	WithUploads(uploads, picks)(app)
	shot, _, err := uploads.Save(t.Context(), "element-1.png", "image/png", []byte("\x89PNG\r\n\x1a\n"))
	if err != nil {
		t.Fatal(err)
	}
	pick, err := picks.Save(t.Context(), valueobject.Pick{ConversationID: "ann-room", Marker: 1, Upload: shot,
		Descriptor: valueobject.AnnotationDescriptor{Kind: enum.AnnotationElement, TagName: "button", Name: "Sign in"}})
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := app.Annotated(t.Context(), "other-room", []AnnotationInput{{PickID: pick.ID}}); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("foreign pick err = %v, want 400", err)
	}
	if _, _, err := app.Annotated(t.Context(), "ann-room", []AnnotationInput{{PickID: "pk_nope"}}); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("unknown pick err = %v, want 400", err)
	}
	anns, shots, err := app.Annotated(t.Context(), "ann-room", []AnnotationInput{{PickID: pick.ID, Comment: " bigger ", Screenshot: false}})
	if err != nil || len(anns) != 1 || len(shots) != 0 || anns[0].Comment != "bigger" {
		t.Fatalf("no-screenshot = %+v %+v %v", anns, shots, err)
	}
	_, shots, _ = app.Annotated(t.Context(), "ann-room", []AnnotationInput{{PickID: pick.ID, Screenshot: true}})
	if len(shots) != 1 || shots[0].MediaType() != "image/png" {
		t.Fatalf("screenshot = %+v", shots)
	}

	// Annotations alone are a message.
	if _, err := app.Send(t.Context(), "ann-room", "gw1", "m1", "", nil, appjob.SendOptions{Annotations: anns}, nil); err != nil {
		t.Fatal(err)
	}
	conv, err := app.deps.Convs.Load(t.Context(), "ann-room")
	if err != nil {
		t.Fatal(err)
	}
	got := conv.Messages()[0].Annotations()
	if len(got) != 1 || got[0].Descriptor.Name != "Sign in" || got[0].Comment != "bigger" || got[0].Screenshot {
		t.Fatalf("stored annotations = %+v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(body, "annotation-context") || !strings.Contains(body, "Comment: bigger") {
		t.Fatalf("model request lacks the annotation note: %s", body)
	}
}
