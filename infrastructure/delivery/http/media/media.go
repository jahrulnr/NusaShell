// Package media is the WebUI boundary of the media roles the owner calls
// directly: speech to text for the composer's voice input runs on the
// model Settings → Models assigns to transcription.
package media

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// MaxAudio caps one recording: the size speech-to-text surfaces accept.
const MaxAudio = 25 << 20

// Service transcribes speech. *appmedia.App satisfies it.
type Service interface {
	Transcribe(ctx context.Context, req dservice.TranscriptionRequest) (string, error)
}

// Register mounts the routes under /api. A nil service registers nothing.
func Register(api *gin.RouterGroup, svc Service) {
	if svc == nil {
		return
	}
	s := &server{svc: svc}
	api.POST("/transcriptions", s.transcribe)
}

type server struct{ svc Service }

// transcribe takes one recording in the multipart "audio" field and an
// optional "language" (ISO-639-1) and answers {"text": "…"}.
func (s *server) transcribe(c *gin.Context) {
	const slack = 1 << 20 // multipart framing around the recording
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxAudio+slack)
	file, header, err := c.Request.FormFile("audio")
	if err != nil {
		if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
			transport.RespondError(c, apperror.New(413, "the recording is larger than 25 MB", err))
			return
		}
		transport.RespondError(c, apperror.New(400, "send one recording in the \"audio\" field", err))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxAudio+1))
	if err != nil {
		transport.RespondError(c, apperror.New(400, "could not read the recording", err))
		return
	}
	if len(data) > MaxAudio {
		transport.RespondError(c, apperror.New(413, "the recording is larger than 25 MB", nil))
		return
	}
	kind := header.Header.Get("Content-Type")
	if !strings.HasPrefix(kind, "audio/") && !strings.HasPrefix(kind, "video/webm") {
		transport.RespondError(c, apperror.New(415, "the recording must be audio", nil))
		return
	}
	text, err := s.svc.Transcribe(c.Request.Context(), dservice.TranscriptionRequest{
		Audio:    dservice.Media{MediaType: kind, Data: data},
		Language: strings.TrimSpace(c.Request.FormValue("language")),
	})
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"text": text})
}
