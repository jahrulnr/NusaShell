package tools

// show tool: render a file from disk in the UI. file_* handles CRUD,
// show handles display.
//
// All ops return metadata only — the file content is never embedded in
// the tool output. The frontend loads the file via /local-file?path=
// using the path field. This keeps the conversation JSON small and
// avoids dumping useless payloads (base64 media, HTML bodies) into the
// provider request and archived chunks.
//
//	op=html: returns { artifact: { path, width, height, title } } so the
//	  frontend renders it in a sandboxed iframe (fetched on demand).
//	op=image/audio/video/pdf: returns { show: { type, path, name, media_type,
//	  size_bytes } } so the frontend renders inline media (fetched on
//	  demand). Use read_media instead when the model needs pixel access.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"nusashell/domain"
)

const (
	showMaxHTMLBytes  = 256 * 1024 // 256KB ≈ 64k tokens, same cap as old artifacts
	showMaxImageBytes = 10 << 20   // 10 MB
	// Matches the generated-media video cap (application/generated_media.go
	// maxGeneratedVideoBytes). 100 MB covers long AI-generated clips; larger
	// references still go through `read_media` + inline attachment rendering,
	// not the show tool.
	showMaxVideoBytes = 100 << 20 // 100 MB
	// Matches the generated-media audio cap (application/generated_media.go
	// maxGeneratedAudioBytes). 20 MB is well past the natural range for TTS
	// output and short audio references; longer audio still goes through
	// `read_media` + inline attachment rendering, not the show tool.
	showMaxAudioBytes = 20 << 20 // 20 MB
	// Keep PDF previews bounded because the browser loads the file on demand
	// into an embedded viewer. Larger documents should be opened externally or
	// passed through a future document-rendering capability.
	showMaxPDFBytes   = 100 << 20 // 100 MB
	showDefaultWidth  = 720
	showDefaultHeight = 400
)

type showArgs struct {
	Op     string `json:"op"`
	Path   string `json:"path"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func executeShow(argsJSON []byte) (bool, string, error) {
	var args showArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return true, "", fmt.Errorf("invalid args: %w", err)
	}
	op := strings.TrimSpace(args.Op)
	if op == "" {
		return true, "", fmt.Errorf("op is required (html, image, audio, video, or pdf)")
	}
	path := strings.TrimSpace(args.Path)
	if path == "" {
		return true, "", fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(path) {
		return true, "", fmt.Errorf("path must be absolute, got %q", path)
	}

	switch op {
	case "html":
		return showHTML(args, path)
	case "image":
		return showImage(path)
	case "audio":
		return showAudio(path)
	case "video":
		return showVideo(path)
	case "pdf":
		return showPDF(path)
	default:
		return true, "", fmt.Errorf("op must be html, image, audio, video, or pdf (got %q)", op)
	}
}

func showHTML(args showArgs, path string) (bool, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return true, "", fmt.Errorf("file not found: %w", err)
	}
	if info.Size() > showMaxHTMLBytes {
		return true, "", fmt.Errorf("HTML file too large: %d bytes (max %d); split into smaller files or reuse CDNs", info.Size(), showMaxHTMLBytes)
	}
	width := args.Width
	if width <= 0 {
		width = showDefaultWidth
	}
	height := args.Height
	if height <= 0 {
		height = showDefaultHeight
	}
	// Return artifact metadata for the frontend's parseArtifactOutput +
	// renderArtifactCard pipeline. The HTML body is NOT included — it
	// would bloat the conversation JSON (up to 256KB per call) and is
	// useless to the LLM. The frontend fetches the file via
	// /local-file?path= when the user opens the iframe.
	type artifactOut struct {
		Path   string `json:"path"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Title  string `json:"title"`
	}
	title := filepath.Base(path)
	b, _ := json.Marshal(map[string]any{
		"artifact": artifactOut{
			Path:   path,
			Width:  width,
			Height: height,
			Title:  title,
		},
	})
	return true, string(b), nil
}

// showPDF validates the PDF signature and returns metadata for the frontend's
// native browser PDF viewer. The body is deliberately not embedded in the
// tool result; the frontend fetches the absolute path through /local-file when
// it renders the iframe.
func showPDF(path string) (bool, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return true, "", fmt.Errorf("file not found: %w", err)
	}
	if info.IsDir() {
		return true, "", fmt.Errorf("PDF path is a directory: %s", path)
	}
	if info.Size() > showMaxPDFBytes {
		return true, "", fmt.Errorf("PDF file too large: %d bytes (max %d)", info.Size(), showMaxPDFBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return true, "", err
	}
	defer f.Close()
	header := make([]byte, 512)
	n, err := f.Read(header)
	if err != nil && err != io.EOF {
		return true, "", err
	}
	mediaType, kind := domain.SniffMagic(header[:n])
	if mediaType != "application/pdf" || kind != "document" {
		return true, "", fmt.Errorf("unrecognized PDF format (magic bytes did not match application/pdf, got %q)", mediaType)
	}
	b, _ := json.Marshal(map[string]any{
		"show": map[string]any{
			"type":       "pdf",
			"path":       path,
			"name":       filepath.Base(path),
			"media_type": mediaType,
			"size_bytes": info.Size(),
		},
	})
	return true, string(b), nil
}

// showAudio reads an audio file from disk, validates it by binary magic
// number, and returns metadata for the frontend to render an inline player.
// The base64 data URL is NOT included in the tool output — it would bloat
// the conversation JSON and is useless to the LLM (it cannot play audio
// from base64). The frontend loads the file via /local-file?path= using
// the path field, same as generate_speech replays.
func showAudio(path string) (bool, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return true, "", fmt.Errorf("file not found: %w", err)
	}
	if info.Size() > showMaxAudioBytes {
		return true, "", fmt.Errorf("audio file too large: %d bytes (max %d)", info.Size(), showMaxAudioBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return true, "", err
	}
	mediaType, kind := domain.SniffMagic(data)
	if kind != "audio" {
		return true, "", fmt.Errorf(
			"unrecognized audio format (magic bytes did not match any known audio signature, got %q)", mediaType)
	}
	b, _ := json.Marshal(map[string]any{
		"show": map[string]any{
			"type":       "audio",
			"path":       path,
			"name":       filepath.Base(path),
			"media_type": mediaType,
			"size_bytes": info.Size(),
		},
	})
	return true, string(b), nil
}

// showVideo reads a video file from disk, validates it by binary magic
// number, and returns metadata for the frontend to render an inline player.
// The base64 data URL is NOT included in the tool output — it would bloat
// the conversation JSON (a 4.5MB video becomes a 6MB base64 string) and is
// useless to the LLM (it cannot play video from base64). The frontend loads
// the file via /local-file?path= using the path field, same as
// generate_video replays.
func showVideo(path string) (bool, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return true, "", fmt.Errorf("file not found: %w", err)
	}
	if info.Size() > showMaxVideoBytes {
		return true, "", fmt.Errorf("video file too large: %d bytes (max %d)", info.Size(), showMaxVideoBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return true, "", err
	}
	mediaType, kind := domain.SniffMagic(data)
	if kind != "video" {
		return true, "", fmt.Errorf(
			"unrecognized video format (magic bytes did not match any known video signature, got %q)", mediaType)
	}
	b, _ := json.Marshal(map[string]any{
		"show": map[string]any{
			"type":       "video",
			"path":       path,
			"name":       filepath.Base(path),
			"media_type": mediaType,
			"size_bytes": info.Size(),
		},
	})
	return true, string(b), nil
}

// showImage reads an image file from disk, validates it by binary magic
// number, and returns metadata for the frontend to render an inline image.
// The base64 data URL is NOT included in the tool output — it would bloat
// the conversation JSON (a 5MB image becomes a ~6.7MB base64 string) and
// is useless to non-vision LLMs. Vision models that need to see the image
// should use read_media instead, which routes pixels through the
// attachment pipeline. The frontend loads the file via /local-file?path=
// using the path field, same as generate_image replays.
func showImage(path string) (bool, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return true, "", fmt.Errorf("file not found: %w", err)
	}
	if info.Size() > showMaxImageBytes {
		return true, "", fmt.Errorf("image file too large: %d bytes (max %d)", info.Size(), showMaxImageBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return true, "", err
	}
	mediaType, _ := domain.SniffMagic(data)
	if mediaType == "" {
		return true, "", fmt.Errorf("unrecognized image format (magic bytes did not match any known image signature)")
	}
	b, _ := json.Marshal(map[string]any{
		"show": map[string]any{
			"type":       "image",
			"path":       path,
			"name":       filepath.Base(path),
			"media_type": mediaType,
			"size_bytes": info.Size(),
		},
	})
	return true, string(b), nil
}
