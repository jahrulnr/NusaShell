package repoconversation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

type ToolOutputRepository struct{ root string }

func NewToolOutputRepository(root string) (drepo.ToolOutput, error) {
	if root == "" {
		return nil, fmt.Errorf("tool output root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("tool output root: %w", err)
	}
	return &ToolOutputRepository{root: abs}, nil
}

type toolOutputSpill struct{ path string }

func (s toolOutputSpill) Reference() string { return s.path }

func (s toolOutputSpill) Open() (io.WriteCloser, error) {
	return os.OpenFile(s.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
}

func (r *ToolOutputRepository) Reserve(ctx context.Context, conversationID, callID string) (drepo.ToolOutputSpill, error) {
	dir, err := r.outputDir(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("nusashell-exec-%s-%s.log", rand.Text(), safeName(callID)))
	return toolOutputSpill{path: path}, nil
}

// mediaExt names stored media for its type, so a kept file opens as what
// it is wherever its path is shown or reused.
var mediaExt = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp",
	"image/gif": ".gif", "image/svg+xml": ".svg",
	"video/mp4": ".mp4", "video/webm": ".webm", "video/quicktime": ".mov",
	"audio/mpeg": ".mp3", "audio/wav": ".wav", "audio/ogg": ".ogg",
	"audio/flac": ".flac", "audio/aac": ".aac", "audio/opus": ".opus",
	"application/pdf": ".pdf",
}

func (r *ToolOutputRepository) Keep(ctx context.Context, conversationID, callID, mediaType string, data []byte) (valueobject.Attachment, error) {
	base, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return valueobject.Attachment{}, fmt.Errorf("media type %q: %w", mediaType, err)
	}
	dir, err := r.outputDir(ctx, conversationID)
	if err != nil {
		return valueobject.Attachment{}, err
	}
	ext, ok := mediaExt[base]
	if !ok {
		ext = ".bin"
	}
	kind, _, _ := strings.Cut(base, "/")
	name := fmt.Sprintf("%s-%s-%s%s", kind, safeName(callID), strings.ToLower(rand.Text()[:8]), ext)
	path := filepath.Join(dir, name)
	// Exclusive create: a planted file or link at the fresh name fails
	// the write instead of being followed or overwritten.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return valueobject.Attachment{}, err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return valueobject.Attachment{}, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return valueobject.Attachment{}, err
	}
	att, err := valueobject.NewAttachment("file://"+path, mediaType)
	if err != nil {
		return valueobject.Attachment{}, err
	}
	sum := sha256.Sum256(data)
	att.SetSHA256(hex.EncodeToString(sum[:]))
	att.SetName(name)
	return att, nil
}

// outputDir checks that the conversation is a real directory of the
// store and makes its tool-output folder, refusing symlinks that would
// land a write outside the store.
func (r *ToolOutputRepository) outputDir(ctx context.Context, conversationID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := validID(conversationID); err != nil {
		return "", err
	}
	convDir := filepath.Join(r.root, conversationID)
	// Stat follows links. A conversation directory that is itself a
	// symlink would make the spill Mkdir land at the link target, outside
	// the store. Lstat sees the link; only a real directory is a room.
	info, err := os.Lstat(convDir)
	if err != nil {
		return "", fmt.Errorf("exec conversation %s unavailable: %w", conversationID, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("exec conversation %s is a symlink", conversationID)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("exec conversation %s is not a directory", conversationID)
	}
	spillDir := filepath.Join(convDir, toolOutputDir)
	if err := os.Mkdir(spillDir, 0o700); err != nil && !os.IsExist(err) {
		return "", fmt.Errorf("exec output dir: %w", err)
	}
	// A planted symlink would redirect the spill outside the store —
	// Mkdir's EEXIST only says the name is taken, and the link's
	// target can live anywhere. Lstat does not follow it. Other
	// non-directories cannot redirect a write: the exclusive create
	// on the joined path fails, and the caller reports that failure
	// through the spill it reserved.
	if info, err := os.Lstat(spillDir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("exec output dir %s is a symlink", spillDir)
	}
	return spillDir, nil
}

func safeName(callID string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, callID)
}
