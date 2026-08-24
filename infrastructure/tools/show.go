package tools

// show tool: render a file from disk in the UI. file_* handles CRUD,
// show handles display.
//
// op=html: reads an HTML file and returns artifact JSON so the frontend
//   renders it in a sandboxed iframe.
// op=image: reads an image file and returns a data URL so the frontend
//   renders it inline.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"nusashell/application"
	"nusashell/domain"
)

const (
	showMaxHTMLBytes  = 256 * 1024 // 256KB ≈ 64k tokens, same cap as old artifacts
	showMaxImageBytes = 10 << 20   // 10 MB
	showDefaultWidth  = 720
	showDefaultHeight = 400
)

func showToolInfo() application.ToolInfo {
	return application.ToolInfo{
		Name: "show",
		Description: "Render a file from disk in the UI. op=html reads an HTML file and " +
			"displays it in a sandboxed iframe (for prototypes, dashboards, visualizations, " +
			"minigames — write the file first with file_write, then show it). op=image reads " +
			"an image file and displays it inline. Use file_write to create the file, " +
			"file_patch to update it, file_read to inspect it — show only handles display.",
		InputSchema: obj("object", props(
			"op", strEnum("Display type: html (iframe) or image (inline)", "html", "image"),
			"path", str("Absolute path to the file to display"),
			"width", intSchema("Iframe width in pixels (html only, default 720)"),
			"height", intSchema("Iframe height in pixels (html only, default 400)"),
		), "op", "path"),
	}
}

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
		return true, "", fmt.Errorf("op is required (html or image)")
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
	default:
		return true, "", fmt.Errorf("op must be html or image (got %q)", op)
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
	content, err := os.ReadFile(path)
	if err != nil {
		return true, "", err
	}
	width := args.Width
	if width <= 0 {
		width = showDefaultWidth
	}
	height := args.Height
	if height <= 0 {
		height = showDefaultHeight
	}
	// Return artifact JSON for the frontend's parseArtifactOutput +
	// renderArtifactCard pipeline.
	type artifactOut struct {
		HTML   string `json:"html"`
		CSS    string `json:"css"`
		JS     string `json:"js"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Title  string `json:"title"`
	}
	title := filepath.Base(path)
	b, _ := json.Marshal(map[string]any{
		"artifact": artifactOut{
			HTML:   string(content),
			Width:  width,
			Height: height,
			Title:  title,
		},
	})
	return true, string(b), nil
}

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
	dataURL := "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data)
	b, _ := json.Marshal(map[string]any{
		"show": map[string]any{
			"type": "image",
			"src":  dataURL,
			"path": path,
		},
	})
	return true, string(b), nil
}
