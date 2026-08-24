package application

import (
	"encoding/base64"
	"encoding/json"
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

// sniffMediaKind reads the file_path from args (JSON), opens the file,
// reads the first 32 bytes, and returns the media kind detected by
// domain.SniffMagic ("image", "audio", or "video"). Returns an error
// when file_path is missing, the file cannot be read, or the magic
// bytes do not match any known media format.
func sniffMediaKind(argsJSON []byte) (string, error) {
	var args struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	path := strings.TrimSpace(args.FilePath)
	if path == "" {
		return "", fmt.Errorf("file_path is required")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	head := make([]byte, 32)
	n, err := f.Read(head)
	if err != nil && n == 0 {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	head = head[:n]
	_, kind := domain.SniffMagic(head)
	if kind == "" {
		return "", fmt.Errorf("unrecognized media type: %s (binary magic number not recognized)", path)
	}
	return kind, nil
}

// loadMediaAttachment loads any media file directly from disk by absolute
// path and wraps it as an inline data-URL attachment. The filesystem is the
// single source of truth: no conversation-history lookup happens here, so
// paths outside the workspace and attachment store work identically to
// attached files.
//
// File type is validated by binary magic number (domain.SniffMagic), not
// by file extension. Extensions can be lied about (e.g. a .js file
// renamed to .png); magic bytes cannot. When the sniffed kind does not
// match the expected kind, or the bytes do not match any known media
// signature, the file is rejected.
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

	// Validate file type by binary magic number. This is the security
	// guard: a file claiming to be an image by extension but containing
	// JavaScript (or any non-media payload) is rejected here.
	sniffedType, sniffedKind := domain.SniffMagic(data)
	if sniffedKind == "" {
		return domain.Attachment{}, fmt.Errorf(
			"%s file %s is not a valid %s file: binary magic number not recognized (file may be corrupted or misnamed)",
			kind, path, kind)
	}
	if sniffedKind != kind {
		return domain.Attachment{}, fmt.Errorf(
			"%s file %s is not a valid %s file: binary magic identifies it as %s (%s)",
			kind, path, kind, sniffedKind, sniffedType)
	}

	// Use the sniffed media type (from magic bytes) as the authoritative
	// type. Fall back to extension-based MIME only when the sniffer
	// returns a type but it's empty (shouldn't happen, but defensive).
	mediaType := sniffedType
	if mediaType == "" {
		mediaType = mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
		if mediaType == "" || mediaType == "application/octet-stream" {
			mediaType = "application/octet-stream"
		} else if i := strings.Index(mediaType, ";"); i >= 0 {
			mediaType = mediaType[:i]
		}
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
