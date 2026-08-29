package application

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"nusashell/application/service/mediaread"
	"nusashell/domain"
)

// executeReadDocument handles read_media when the sniffed kind is "document"
// (PDF). It loads the PDF from disk by absolute path and returns it as a
// file attachment so the provider adapter can send it via the native
// document/file content part (Anthropic `document`, OpenAI `input_file`,
// OpenRouter `file`). For non-document-capable models, the attachment is
// stripped by filterToolAttachmentsByCaps and a placeholder note is
// returned instead.
func (a *App) executeReadDocument(run *TurnRun, toolCall domain.ToolCall, caps ModelCapabilities, settings domain.Settings) (string, []domain.Attachment, error) {
	var args struct {
		FilePath string `json:"file_path"`
		Question string `json:"question"`
	}
	if err := json.Unmarshal([]byte(toolCall.Args), &args); err != nil {
		return "error: invalid arguments", nil, fmt.Errorf("invalid args: %w", err)
	}
	path := strings.TrimSpace(args.FilePath)
	if path == "" {
		return "error: file_path is required", nil, fmt.Errorf("file_path is required")
	}
	if !filepath.IsAbs(path) {
		return "error: file_path must be an absolute path", nil, fmt.Errorf("file_path must be absolute, got %q", path)
	}

	doc, err := mediaread.LoadMediaAttachment("document", path)
	if err != nil {
		return "error: " + err.Error(), nil, err
	}

	// The attachment type must be "file" (not "document") to match the
	// provider adapter's content-block switch (case "file" → document
	// input_file / document block). SniffMagic returns kind="document"
	// for routing, but the wire attachment type is "file".
	doc.Type = "file"

	summary := "Document loaded into your context."
	if doc.FilePath != "" {
		summary += " File path: " + doc.FilePath
	}
	return summary, []domain.Attachment{doc}, nil
}
