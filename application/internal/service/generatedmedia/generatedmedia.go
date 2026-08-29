// Package generatedmedia is the single persistence path for generate_image /
// generate_speech / generate_video output. It validates bytes by binary
// magic number against the expected kind, enforces per-kind size caps,
// writes to the attachment store, and wraps the result as an inline
// data-URL attachment. Extracted from the application root so the media
// generators depend on a small leaf package instead of the whole
// application package.
package generatedmedia

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode"

	"nusashell/domain"
)

// Per-kind size limits. The caps map is the lookup used at runtime so all
// three generators share one path.
const (
	MaxGeneratedImageBytes = 8 << 20
	MaxGeneratedAudioBytes = 20 << 20
	MaxGeneratedVideoBytes = 100 << 20
)

// Caps caps the size of persisted generated output per kind
// ("image" | "audio" | "video"). A var so tests can narrow it.
var Caps = map[string]int64{
	"image": MaxGeneratedImageBytes,
	"audio": MaxGeneratedAudioBytes,
	"video": MaxGeneratedVideoBytes,
}

// CapLabels renders each cap in human units for error text.
var CapLabels = map[string]string{
	"image": "8 MiB",
	"audio": "20 MB",
	"video": "100 MB",
}

// Store is the persistence port for writing generated media. The
// application root's AttachmentStore satisfies this implicitly.
type Store interface {
	WriteBytes(conversationID, name string, data []byte) (string, error)
}

// Save validates the bytes by binary magic number against the expected
// kind (a corrupted or mislabeled payload from an upstream provider is
// rejected here), enforces the per-kind size cap, writes to the
// attachment store, and wraps the result as an inline data-URL
// attachment. baseName is sanitized and the extension is derived from
// the sniffed media type. When inline is false the DataURL is left empty
// (the UI loads the file from FilePath instead).
func Save(store Store, conversationID, baseName, kind string, data []byte, inline bool) (domain.Attachment, string, error) {
	if len(data) == 0 {
		return domain.Attachment{}, "", fmt.Errorf("%s generation returned no data", kind)
	}
	capBytes, ok := Caps[kind]
	if !ok {
		return domain.Attachment{}, "", fmt.Errorf("unknown media kind %q", kind)
	}
	if int64(len(data)) > capBytes {
		return domain.Attachment{}, "", fmt.Errorf("generated %s exceeds the %s limit (%d bytes)", kind, CapLabels[kind], len(data))
	}
	if store == nil {
		return domain.Attachment{}, "", fmt.Errorf("attachment store is not configured")
	}
	sniffedType, sniffedKind := domain.SniffMagic(data)
	if sniffedKind != kind {
		return domain.Attachment{}, "", fmt.Errorf(
			"generated %s failed binary validation: magic number identifies it as %q (%q)",
			kind, sniffedKind, sniffedType)
	}
	name := SanitizeFilePart(baseName) + ExtForSniffedMedia(sniffedType)
	path, err := store.WriteBytes(conversationID, name, data)
	if err != nil {
		return domain.Attachment{}, "", err
	}
	att := domain.Attachment{Type: kind, Name: name, MediaType: sniffedType, FilePath: path}
	if inline {
		// Inline data URL for tools that deliver the media straight to the
		// UI in the tool result (speech, video). Image persistence leaves
		// it empty on purpose — the UI loads the file from FilePath instead.
		att.DataURL = fmt.Sprintf("data:%s;base64,%s", sniffedType, base64.StdEncoding.EncodeToString(data))
	}
	return att, path, nil
}

// ExtForSniffedMedia maps a sniffed media type to a canonical extension.
func ExtForSniffedMedia(mediaType string) string {
	switch strings.ToLower(mediaType) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/ogg":
		return ".ogg"
	case "audio/mp4":
		return ".m4a"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/x-msvideo":
		return ".avi"
	case "video/quicktime":
		return ".mov"
	default:
		return ".bin"
	}
}

// SanitizeFilePart replaces every character that is not a letter, digit,
// '-', '_', or '.' with '_'. Used to derive safe file names from tool
// call IDs and user-provided base names.
func SanitizeFilePart(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
