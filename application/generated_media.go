package application

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"nusashell/domain"
)

// Generated-media size limits, named for callers/tests; the caps map is
// the lookup used at runtime so all three generators share one path.
const (
	maxGeneratedImageBytes = 8 << 20
	maxGeneratedAudioBytes = 20 << 20
	maxGeneratedVideoBytes = 100 << 20
)

// generatedMediaCaps caps the size of persisted generated output per kind
// ("image" | "audio" | "video"). A var so tests can narrow it.
var generatedMediaCaps = map[string]int64{
	"image": maxGeneratedImageBytes,
	"audio": maxGeneratedAudioBytes,
	"video": maxGeneratedVideoBytes,
}

// generatedMediaCapLabels renders each cap in human units for error text.
var generatedMediaCapLabels = map[string]string{
	"image": "8 MiB",
	"audio": "20 MB",
	"video": "100 MB",
}

// saveGeneratedMedia is the single persistence path for generate_image /
// generate_speech / generate_video output: it validates the bytes by
// binary magic number against the expected kind (a corrupted or mislabeled
// payload from an upstream provider is rejected here), enforces the
// per-kind size cap, writes to the attachment store, and wraps the result
// as an inline data-URL attachment. baseName is sanitized and the
// extension is derived from the sniffed media type.
func (a *App) saveGeneratedMedia(conversationID, baseName, kind string, data []byte, inline bool) (domain.Attachment, string, error) {
	if len(data) == 0 {
		return domain.Attachment{}, "", fmt.Errorf("%s generation returned no data", kind)
	}
	capBytes, ok := generatedMediaCaps[kind]
	if !ok {
		return domain.Attachment{}, "", fmt.Errorf("unknown media kind %q", kind)
	}
	if int64(len(data)) > capBytes {
		return domain.Attachment{}, "", fmt.Errorf("generated %s exceeds the %s limit (%d bytes)", kind, generatedMediaCapLabels[kind], len(data))
	}
	if a.Attachments == nil {
		return domain.Attachment{}, "", fmt.Errorf("attachment store is not configured")
	}
	sniffedType, sniffedKind := domain.SniffMagic(data)
	if sniffedKind != kind {
		return domain.Attachment{}, "", fmt.Errorf(
			"generated %s failed binary validation: magic number identifies it as %q (%q)",
			kind, sniffedKind, sniffedType)
	}
	name := sanitizeFilePart(baseName) + extForSniffedMedia(sniffedType)
	path, err := a.Attachments.WriteBytes(conversationID, name, data)
	if err != nil {
		return domain.Attachment{}, "", err
	}
	att := domain.Attachment{Type: kind, Name: name, MediaType: sniffedType, FilePath: path}
	if inline {
		// Inline data URL for tools that deliver the media straight to the
		// UI in the tool result (speech, video). Image persistence leaves it
		// empty on purpose — the UI loads the file from FilePath instead.
		att.DataURL = fmt.Sprintf("data:%s;base64,%s", sniffedType, base64.StdEncoding.EncodeToString(data))
	}
	return att, path, nil
}

// extForSniffedMedia maps a sniffed media type to a canonical extension.
func extForSniffedMedia(mediaType string) string {
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

// executeGenerateMedia routes the unified generate_media tool call to the
// mode-specific executor based on media_type. Legacy tool names from older
// conversations (generate_image / generate_speech / generate_video) remain
// accepted as aliases so replayed history keeps running.
func (a *App) executeGenerateMedia(run *TurnRun, toolCall domain.ToolCall, settings domain.Settings) (string, []domain.Attachment, error) {
	var head struct {
		MediaType string `json:"media_type"`
	}
	_ = json.Unmarshal([]byte(toolCall.Args), &head)
	mode := strings.ToLower(strings.TrimSpace(head.MediaType))
	if mode == "" {
		switch toolCall.Name { // legacy alias without an explicit media_type
		case "generate_video":
			mode = "video"
		case "generate_speech":
			mode = "speech"
		default:
			mode = "image"
		}
	}
	switch mode {
	case "speech":
		return a.executeGenerateSpeech(run, toolCall, settings)
	case "video":
		return a.executeGenerateVideo(run, toolCall, settings)
	default:
		return a.executeGenerateImage(run, toolCall, settings)
	}
}
