package media

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/gin-gonic/gin"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

type fakeSTT struct {
	got dservice.TranscriptionRequest
	err error
}

func (f *fakeSTT) Transcribe(_ context.Context, req dservice.TranscriptionRequest) (string, error) {
	f.got = req
	return "halo dunia", f.err
}

func post(t *testing.T, svc Service, kind string, data []byte, field string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r.Group("/api"), svc)
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="`+field+`"; filename="voice.webm"`)
	h.Set("Content-Type", kind)
	part, err := w.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	part.Write(data)
	w.WriteField("language", " id ")
	w.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/transcriptions", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestTranscribeAnswersText(t *testing.T) {
	svc := &fakeSTT{}
	rec := post(t, svc, "audio/webm;codecs=opus", []byte("sound"), "audio")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	var out struct{ Text string }
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Text != "halo dunia" {
		t.Fatalf("text = %q", out.Text)
	}
	if svc.got.Audio.MediaType != "audio/webm;codecs=opus" || string(svc.got.Audio.Data) != "sound" || svc.got.Language != "id" {
		t.Fatalf("request = %+v", svc.got)
	}
}

func TestTranscribeRefusesWhatIsNotAudio(t *testing.T) {
	if rec := post(t, &fakeSTT{}, "text/plain", []byte("x"), "audio"); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec := post(t, &fakeSTT{}, "audio/webm", []byte("x"), "file"); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestTranscribePassesTheRoleError(t *testing.T) {
	svc := &fakeSTT{err: apperror.New(409, "no speech-to-text model is set", nil)}
	if rec := post(t, svc, "audio/webm", []byte("x"), "audio"); rec.Code != http.StatusConflict {
		t.Fatalf("status = %d", rec.Code)
	}
}
