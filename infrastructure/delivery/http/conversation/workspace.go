package conversation

import (
	"context"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// WorkspaceSurface is what the composer and the side pane read about the
// folders a room works in. *appconversation.ConversationApp satisfies it.
type WorkspaceSurface interface {
	Roots(ctx context.Context, p appconversation.Place) (drepo.Workspace, []string, error)
	ReferenceCandidates(ctx context.Context, p appconversation.Place, query string) ([]appconversation.ReferenceCandidate, bool, error)
	OpenFile(ctx context.Context, p appconversation.Place, path string) (io.ReadCloser, fs.FileInfo, string, error)
}

type workspace struct{ svc WorkspaceSurface }

func registerWorkspace(api *gin.RouterGroup, svc WorkspaceSurface) {
	w := &workspace{svc: svc}
	api.GET("/references", w.references)
	api.GET("/roots", w.roots)
	api.GET("/files", w.file)
	api.GET("/conversations/:id/files", w.file)
}

// place reads where the owner is composing from the query: a room, or a
// draft's project or workspace.
func place(c *gin.Context) (appconversation.Place, bool) {
	p := appconversation.Place{
		ConversationID: strings.TrimSpace(c.Query("conversationId")),
		ProjectID:      strings.TrimSpace(c.Query("projectId")),
		Workspace:      strings.TrimSpace(c.Query("workspace")),
	}
	for _, id := range []string{p.ConversationID, p.ProjectID} {
		if id == "" {
			continue
		}
		if err := valueobject.ValidID(id); err != nil {
			transport.RespondError(c, apperror.New(400, "invalid id", err))
			return p, false
		}
	}
	return p, true
}

func (w *workspace) references(c *gin.Context) {
	p, ok := place(c)
	if !ok {
		return
	}
	items, truncated, err := w.svc.ReferenceCandidates(c.Request.Context(), p, c.Query("q"))
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	if items == nil {
		items = []appconversation.ReferenceCandidate{}
	}
	c.JSON(200, gin.H{"items": items, "truncated": truncated})
}

func (w *workspace) roots(c *gin.Context) {
	p, ok := place(c)
	if !ok {
		return
	}
	_, roots, err := w.svc.Roots(c.Request.Context(), p)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	if roots == nil {
		roots = []string{}
	}
	c.JSON(200, gin.H{"roots": roots})
}

// localPath — the file: / ~ / %VAR% expansion a linked file needs — lives
// in localfile.go, next to the helper every surface now shares.

// file streams a linked file. Media and PDFs open inline; anything else,
// or download=1, downloads. Range requests work, so a preview can read
// just the head of a large file.
func (w *workspace) file(c *gin.Context) {
	p, ok := place(c)
	if !ok {
		return
	}
	if id := c.Param("id"); id != "" {
		if err := valueobject.ValidID(id); err != nil {
			transport.RespondError(c, apperror.New(400, "invalid conversation id", err))
			return
		}
		p = appconversation.Place{ConversationID: id}
	}
	path, err := localPath(c.Query("path"))
	if err != nil {
		transport.RespondError(c, apperror.New(400, err.Error(), err))
		return
	}
	rc, info, path, err := w.svc.OpenFile(c.Request.Context(), p, path)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	defer rc.Close()
	name := filepath.Base(path)
	kind := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	seeker, canSeek := rc.(io.ReadSeeker)
	if kind == "" && canSeek {
		var head [512]byte
		n, _ := io.ReadFull(seeker, head[:])
		kind = http.DetectContentType(head[:n])
		if _, err := seeker.Seek(0, io.SeekStart); err != nil {
			transport.RespondError(c, err)
			return
		}
	}
	if kind == "" {
		kind = "application/octet-stream"
	}
	disposition := "attachment"
	if c.Query("download") != "1" && (strings.HasPrefix(kind, "image/") || strings.HasPrefix(kind, "video/") || strings.HasPrefix(kind, "audio/") || kind == "application/pdf") {
		disposition = "inline"
	}
	h := c.Writer.Header()
	h.Set("Content-Type", kind)
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	// A served file never runs as a page of this origin (an SVG opened in
	// a tab would). The browser's PDF viewer does not work sandboxed.
	if kind != "application/pdf" {
		h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'; sandbox")
	}
	if canSeek {
		http.ServeContent(c.Writer, c.Request, name, info.ModTime(), seeker)
		return
	}
	h.Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	_, _ = io.Copy(c.Writer, rc)
}
