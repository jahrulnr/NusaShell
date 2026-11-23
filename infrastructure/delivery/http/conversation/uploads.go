package conversation

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Uploader keeps files the owner attaches before sending.
// *appconversation.ConversationApp satisfies it.
type Uploader interface {
	Upload(ctx context.Context, name, mediaType string, data []byte) (appconversation.Upload, error)
}

// upload takes one multipart "file" and answers 201 with its id.
func upload(svc Uploader) gin.HandlerFunc {
	return func(c *gin.Context) {
		const slack = 1 << 20 // multipart framing around the file
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, appconversation.MaxUpload+slack)
		file, header, err := c.Request.FormFile("file")
		if err != nil {
			if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
				transport.RespondError(c, apperror.New(413, "the file is larger than 25 MB", err))
				return
			}
			transport.RespondError(c, apperror.New(400, "send one file in the \"file\" field", err))
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, appconversation.MaxUpload+1))
		if err != nil {
			transport.RespondError(c, apperror.New(400, "could not read the file", err))
			return
		}
		up, err := svc.Upload(c.Request.Context(), header.Filename, header.Header.Get("Content-Type"), data)
		if err != nil {
			transport.RespondError(c, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"upload": up})
	}
}
