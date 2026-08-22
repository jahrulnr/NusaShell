package application

import (
	"encoding/base64"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"nusashell/domain"
)

// maxReadMediaBytes caps the size of a media file loaded by the read_*
// tools into an inline data URL attachment. A var so tests can narrow it.
var maxReadMediaBytes int64 = 100 << 20 // 100 MB

// loadMediaAttachment loads any media file directly from disk by absolute
// path and wraps it as an inline data-URL attachment. The filesystem is the
// single source of truth: no conversation-history lookup happens here, so
// paths outside the workspace and attachment store work identically to
// attached files.
func loadMediaAttachment(kind, path string) (domain.Attachment, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.Attachment{}, fmt.Errorf("%s file not found on disk: %s", kind, path)
		}
		return domain.Attachment{}, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return domain.Attachment{}, fmt.Errorf("path is a directory, not a %s file: %s", kind, path)
	}
	if info.Size() > maxReadMediaBytes {
		return domain.Attachment{}, fmt.Errorf(
			"%s file is %d bytes, exceeding the %d byte limit for inline loading",
			kind, info.Size(), maxReadMediaBytes)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return domain.Attachment{}, err
	}

	mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if mediaType == "" || mediaType == "application/octet-stream" {
		mediaType = "application/octet-stream"
	} else if i := strings.Index(mediaType, ";"); i >= 0 {
		mediaType = mediaType[:i]
	}

	name := filepath.Base(path)
	return domain.Attachment{
		Type:      kind,
		Name:      name,
		MediaType: mediaType,
		DataURL:   fmt.Sprintf("data:%s;base64,%s", mediaType, base64.StdEncoding.EncodeToString(data)),
		FilePath:  path,
	}, nil
}
