package appconversation

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// MaxUpload bounds one file the owner attaches to a message.
const MaxUpload = 25 << 20

// maxUploadsPerMessage bounds how many files one message carries.
const maxUploadsPerMessage = 10

// maxAnnotationsPerMessage bounds how many live-page marks one message
// carries.
const maxAnnotationsPerMessage = 20

// WithUploads sets where owner uploads and live-page picks are kept.
func WithUploads(u drepo.Uploads, p drepo.Picks) Option {
	return func(app *ConversationApp) { app.uploads, app.picks = u, p }
}

// UseRecordings sets where browser recordings a message can carry are
// read; the store opens after the app, so it is a late bind.
func (app *ConversationApp) UseRecordings(r drepo.Recordings) {
	app.recordings = r
}

// Upload is a stored file ready to ride a message.
type Upload struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	Size      int    `json:"size"`
}

// Upload keeps one file for a message the owner is about to send. A
// missing or generic media type is sniffed from the name, then the bytes.
func (app *ConversationApp) Upload(ctx context.Context, name, mediaType string, data []byte) (Upload, error) {
	if app.uploads == nil {
		return Upload{}, apperror.New(503, "uploads unavailable", nil)
	}
	if len(data) == 0 {
		return Upload{}, apperror.New(400, "the file is empty", nil)
	}
	if len(data) > MaxUpload {
		return Upload{}, apperror.New(413, "the file is larger than 25 MB", nil)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "upload"
	}
	if mt, _, err := mime.ParseMediaType(mediaType); err != nil || mt == "application/octet-stream" {
		mediaType = mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
		if mediaType == "" {
			mediaType = http.DetectContentType(data)
		}
	}
	id, a, err := app.uploads.Save(ctx, name, mediaType, data)
	if err != nil {
		return Upload{}, err
	}
	return Upload{ID: id, Name: a.Name(), MediaType: a.MediaType(), Size: len(data)}, nil
}

// Uploaded resolves the uploads a message names to its attachments.
func (app *ConversationApp) Uploaded(ctx context.Context, ids []string) ([]valueobject.Attachment, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if app.uploads == nil {
		return nil, apperror.New(503, "uploads unavailable", nil)
	}
	if len(ids) > maxUploadsPerMessage {
		return nil, apperror.New(400, "a message carries at most 10 files", nil)
	}
	out := make([]valueobject.Attachment, 0, len(ids))
	for _, id := range ids {
		a, err := app.uploads.Get(ctx, id)
		if apperror.As(err).StatusCode() == apperror.StatusNotFound {
			return nil, apperror.New(400, "unknown upload "+id, err)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// AnnotationInput is one pick a message carries: the owner's comment and
// whether its marked screenshot goes with it.
type AnnotationInput struct {
	PickID     string
	Comment    string
	Screenshot bool
}

// Annotated resolves the picks a message names to its annotations and the
// screenshots that ride with them, in order. A pick made in another
// thread is refused like an unknown one.
func (app *ConversationApp) Annotated(ctx context.Context, convID string, in []AnnotationInput) ([]valueobject.Annotation, []valueobject.Attachment, error) {
	if len(in) == 0 {
		return nil, nil, nil
	}
	if app.picks == nil || app.uploads == nil {
		return nil, nil, apperror.New(503, "annotations unavailable", nil)
	}
	if len(in) > maxAnnotationsPerMessage {
		return nil, nil, apperror.New(400, "a message carries at most 20 annotations", nil)
	}
	anns := make([]valueobject.Annotation, 0, len(in))
	var shots []valueobject.Attachment
	for _, a := range in {
		p, err := app.picks.Get(ctx, a.PickID)
		if apperror.As(err).StatusCode() == apperror.StatusNotFound || (err == nil && p.ConversationID != convID) {
			return nil, nil, apperror.New(400, "unknown annotation "+a.PickID, err)
		}
		if err != nil {
			return nil, nil, err
		}
		if a.Screenshot {
			shot, err := app.uploads.Get(ctx, p.Upload)
			if err != nil {
				return nil, nil, err
			}
			shots = append(shots, shot)
		}
		anns = append(anns, valueobject.Annotation{Marker: p.Marker, Comment: strings.TrimSpace(a.Comment), Screenshot: a.Screenshot, Descriptor: p.Descriptor})
	}
	return anns, shots, nil
}

// Recorded resolves the browser recording a message names to its ref —
// the id, status, and its summary as it reads at send time. A recording
// made in another thread is refused like an unknown one. A live
// recording carries a meta brief in place of the summary it writes only
// when stopped.
func (app *ConversationApp) Recorded(ctx context.Context, convID string, id string) (*valueobject.RecordingRef, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	if app.recordings == nil {
		return nil, apperror.New(503, "recordings unavailable", nil)
	}
	r, err := app.recordings.Get(id)
	// Get reports every miss — unknown id and unreadable meta — through
	// one sentinel; a recording from another thread reads the same way.
	if err != nil || r.Meta().ConversationID != convID {
		return nil, apperror.New(400, "unknown recording "+id, err)
	}
	meta := r.Meta()
	ref := &valueobject.RecordingRef{ID: meta.ID, Status: meta.Status}
	summary, err := r.Summary()
	switch {
	case err == nil && strings.TrimSpace(summary) != "":
		ref.Summary = summary
	case meta.Status == "recording" || meta.Status == "paused":
		// A live recording writes its summary at Stop; give the message
		// the meta that exists now instead of a bare "unavailable".
		started := ""
		if meta.StartedAt > 0 {
			started = " · started " + time.UnixMilli(meta.StartedAt).Format(time.RFC3339)
		}
		ref.Summary = fmt.Sprintf("recording %s · status %s%s · %d events, %d annotations, %d errors — summary after stop; use browser_get_timeline",
			meta.ID, meta.Status, started, meta.Counts.Events, meta.Counts.Annotations, meta.Counts.Errors)
	}
	return ref, nil
}
