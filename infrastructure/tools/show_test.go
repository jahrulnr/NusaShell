package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShowHTMLReturnsArtifactJSON(t *testing.T) {
	dir := t.TempDir()
	htmlPath := filepath.Join(dir, "game.html")
	writeFile(t, htmlPath, "<!DOCTYPE html><html><body><h1>Hello</h1></body></html>")

	ok, out, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op":     "html",
		"path":   htmlPath,
		"width":  640,
		"height": 480,
	}))
	if !ok || err != nil {
		t.Fatalf("show failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, `"artifact"`) {
		t.Errorf("output should contain artifact key, got: %s", out)
	}
	if !contains(out, "Hello") {
		t.Errorf("output should contain HTML content, got: %s", out)
	}
	if !contains(out, `"width":640`) {
		t.Errorf("output should contain width, got: %s", out)
	}
	if !contains(out, `"height":480`) {
		t.Errorf("output should contain height, got: %s", out)
	}
}

func TestShowHTMLDefaultDimensions(t *testing.T) {
	dir := t.TempDir()
	htmlPath := filepath.Join(dir, "page.html")
	writeFile(t, htmlPath, "<p>test</p>")

	ok, out, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op":   "html",
		"path": htmlPath,
	}))
	if !ok || err != nil {
		t.Fatalf("show failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, `"width":720`) || !contains(out, `"height":400`) {
		t.Errorf("default dimensions should be 720x400, got: %s", out)
	}
}

func TestShowHTMLFileNotFound(t *testing.T) {
	_, _, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op":   "html",
		"path": "/nonexistent/file.html",
	}))
	if err == nil {
		t.Error("missing file should error")
	}
}

func TestShowHTMLMissingPath(t *testing.T) {
	_, _, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op": "html",
	}))
	if err == nil {
		t.Error("missing path should error")
	}
}

func TestShowImageReturnsDataURL(t *testing.T) {
	dir := t.TempDir()
	// Minimal valid PNG (8-byte signature + IHDR)
	pngPath := filepath.Join(dir, "chart.png")
	pngData := append(
		[]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A},
		[]byte("\x00\x00\x00\x0DIHDR\x00\x00\x00\x01")...,
	)
	if err := os.WriteFile(pngPath, pngData, 0o644); err != nil {
		t.Fatal(err)
	}

	ok, out, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op":   "image",
		"path": pngPath,
	}))
	if !ok || err != nil {
		t.Fatalf("show failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, `"show"`) {
		t.Errorf("output should contain show key, got: %s", out)
	}
	if !contains(out, `"type":"image"`) {
		t.Errorf("output should contain type:image, got: %s", out)
	}
	if !contains(out, "data:image/png;base64,") {
		t.Errorf("output should contain data URL, got: %s", out)
	}
}

func TestShowImageFileNotFound(t *testing.T) {
	_, _, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op":   "image",
		"path": "/nonexistent/file.png",
	}))
	if err == nil {
		t.Error("missing file should error")
	}
}

func TestShowInvalidOp(t *testing.T) {
	_, _, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op":   "video",
		"path": "/tmp/x",
	}))
	if err == nil {
		t.Error("invalid op should error")
	}
}

func TestShowMissingOp(t *testing.T) {
	dir := t.TempDir()
	htmlPath := filepath.Join(dir, "page.html")
	writeFile(t, htmlPath, "<p>test</p>")

	_, _, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"path": htmlPath,
	}))
	if err == nil {
		t.Error("missing op should error")
	}
}

func TestShowHTMLSizeCap(t *testing.T) {
	dir := t.TempDir()
	htmlPath := filepath.Join(dir, "big.html")
	// Create a file just over the cap
	big := make([]byte, showMaxHTMLBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(htmlPath, big, 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op":   "html",
		"path": htmlPath,
	}))
	if err == nil {
		t.Error("oversized HTML should error")
	}
}
