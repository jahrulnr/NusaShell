package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShowHTMLReturnsMetadata(t *testing.T) {
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
	if !contains(out, `"width":640`) {
		t.Errorf("output should contain width, got: %s", out)
	}
	if !contains(out, `"height":480`) {
		t.Errorf("output should contain height, got: %s", out)
	}
	if !contains(out, `"path":"`+jsonPath(htmlPath)+`"`) {
		t.Errorf("output should include original path, got: %s", out)
	}
	// The HTML body must NOT be in the output — it bloats the conversation
	// JSON and is useless to the LLM. The frontend fetches it via
	// /local-file?path= when the user opens the iframe.
	if contains(out, "Hello") {
		t.Errorf("output must NOT contain HTML body content, got: %s", out)
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

func TestShowPDFReturnsMetadata(t *testing.T) {
	dir := t.TempDir()
	pdfPath := filepath.Join(dir, "report.pdf")
	writeFile(t, pdfPath, "%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n")

	ok, out, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op":   "pdf",
		"path": pdfPath,
	}))
	if !ok || err != nil {
		t.Fatalf("show PDF failed: ok=%v err=%v", ok, err)
	}
	for _, want := range []string{
		`"show"`,
		`"type":"pdf"`,
		`"media_type":"application/pdf"`,
		`"path":"` + jsonPath(pdfPath) + `"`,
		`"size_bytes"`,
	} {
		if !contains(out, want) {
			t.Errorf("PDF output should contain %s, got: %s", want, out)
		}
	}
	if contains(out, "%PDF-1.7") {
		t.Errorf("PDF body must not be embedded in tool output, got: %s", out)
	}
}

func TestShowPDFRejectsNonPDFFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-a-pdf.bin")
	writeFile(t, path, "plain text")

	_, _, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op":   "pdf",
		"path": path,
	}))
	if err == nil {
		t.Fatal("show(op=pdf) should reject a file without PDF magic bytes")
	}
}

func TestShowPDFSizeCap(t *testing.T) {
	dir := t.TempDir()
	pdfPath := filepath.Join(dir, "huge.pdf")
	writeFile(t, pdfPath, "%PDF-1.7\n")
	if err := os.Truncate(pdfPath, showMaxPDFBytes+1); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op":   "pdf",
		"path": pdfPath,
	}))
	if err == nil {
		t.Fatal("oversized PDF should error before opening a browser preview")
	}
}

func TestShowImageReturnsMetadata(t *testing.T) {
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
	if !contains(out, `"path":"`+jsonPath(pngPath)+`"`) {
		t.Errorf("output should include original path, got: %s", out)
	}
	if !contains(out, `"media_type":"image/png"`) {
		t.Errorf("output should include media_type, got: %s", out)
	}
	if !contains(out, `"size_bytes"`) {
		t.Errorf("output should include size_bytes, got: %s", out)
	}
	// The base64 data URL must NOT be in the output — it bloats the
	// conversation JSON and is useless to the LLM. The frontend loads
	// the image via /local-file?path= using the path field.
	if contains(out, "data:image/png;base64,") {
		t.Errorf("output must NOT contain base64 data URL, got: %s", out)
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

// TestShowImageSVG verifies that show(op=image) accepts SVG files. SVG is
// text-based XML (not a binary magic number), so SniffMagic scans for "<svg"
// in the leading bytes. The frontend renders SVG via <img src> which strips
// scripts — safe even for agent-generated SVG.
func TestShowImageSVG(t *testing.T) {
	dir := t.TempDir()
	svgPath := filepath.Join(dir, "diagram.svg")
	svgData := `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100">
  <rect width="200" height="100" fill="#a9c7ff"/>
  <text x="100" y="50" text-anchor="middle">Hello SVG</text>
</svg>`
	if err := os.WriteFile(svgPath, []byte(svgData), 0o644); err != nil {
		t.Fatal(err)
	}

	ok, out, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op":   "image",
		"path": svgPath,
	}))
	if !ok || err != nil {
		t.Fatalf("show(op=image) for SVG failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, `"type":"image"`) {
		t.Errorf("output should contain type:image, got: %s", out)
	}
	if !contains(out, `"media_type":"image/svg+xml"`) {
		t.Errorf("output should include media_type image/svg+xml, got: %s", out)
	}
	if !contains(out, `"path":"`+jsonPath(svgPath)+`"`) {
		t.Errorf("output should include original path, got: %s", out)
	}
}

func TestShowInvalidOp(t *testing.T) {
	_, _, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op":   "spreadsheet",
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

// WAV magic bytes: "RIFF" + 4 bytes file size + "WAVE".
// Minimal valid RIFF/WAVE header (44 bytes).
var wavHeader = []byte{
	0x52, 0x49, 0x46, 0x46, // "RIFF"
	0x24, 0x00, 0x00, 0x00, // file size (placeholder)
	0x57, 0x41, 0x56, 0x45, // "WAVE"
	0x66, 0x6d, 0x74, 0x20, // "fmt "
	0x10, 0x00, 0x00, 0x00, // subchunk size (16 for PCM)
	0x01, 0x00, 0x01, 0x00, 0x44, 0xac, 0x00, 0x00,
	0x88, 0x58, 0x01, 0x00, 0x02, 0x00, 0x10, 0x00,
	0x64, 0x61, 0x74, 0x61, // "data"
	0x00, 0x00, 0x00, 0x00, // data size (placeholder)
}

func TestShowAudioReturnsMetadata(t *testing.T) {
	dir := t.TempDir()
	wavPath := filepath.Join(dir, "speech.wav")
	if err := os.WriteFile(wavPath, wavHeader, 0o644); err != nil {
		t.Fatal(err)
	}

	ok, out, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op":   "audio",
		"path": wavPath,
	}))
	if !ok || err != nil {
		t.Fatalf("show audio failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, `"show"`) {
		t.Errorf("output should contain show key, got: %s", out)
	}
	if !contains(out, `"type":"audio"`) {
		t.Errorf("output should set type=audio, got: %s", out)
	}
	if !contains(out, `"path":"`+jsonPath(wavPath)+`"`) {
		t.Errorf("output should include original path, got: %s", out)
	}
	if !contains(out, `"media_type":"audio/wav"`) {
		t.Errorf("output should include media_type, got: %s", out)
	}
	if !contains(out, `"size_bytes"`) {
		t.Errorf("output should include size_bytes, got: %s", out)
	}
	// The base64 data URL must NOT be in the output — the frontend loads
	// the audio via /local-file?path= using the path field.
	if contains(out, "data:audio/wav;base64,") {
		t.Errorf("output must NOT contain base64 data URL, got: %s", out)
	}
}

func TestShowAudioRejectsNonAudioFile(t *testing.T) {
	dir := t.TempDir()
	notAudio := filepath.Join(dir, "not_audio.txt")
	// PNG magic bytes — not an audio file.
	if err := os.WriteFile(notAudio, []byte{0x89, 0x50, 0x4e, 0x47}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op":   "audio",
		"path": notAudio,
	}))
	if err == nil {
		t.Error("non-audio file should error")
	}
}

func TestShowAudioSizeCap(t *testing.T) {
	dir := t.TempDir()
	bigPath := filepath.Join(dir, "huge.wav")
	// Real WAV header followed by junk past the cap. We only need SniffMagic
	// to recognize the file; the size check fires before the body is read.
	big := append([]byte{}, wavHeader...)
	big = append(big, make([]byte, showMaxAudioBytes+1)...)
	if err := os.WriteFile(bigPath, big, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op":   "audio",
		"path": bigPath,
	}))
	if err == nil {
		t.Error("oversized audio should error")
	}
}

func TestShowInvalidOpRejectsAudioWrongCase(t *testing.T) {
	// Only the documented ops are accepted. Confirms the switch's default
	// branch still fires for unknown ops, even ones containing "audio".
	_, _, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op":   "Audio",
		"path": "/tmp/x",
	}))
	if err == nil {
		t.Error("op=Audio (capital A) should error")
	}
}

// MP4 magic bytes: a "ftyp" box at offset 4 with brand "isom". The first
// 32 bytes are enough to satisfy SniffMagic without claiming the file is
// a fully valid MP4 (we only test that the backend accepts it).
var mp4Header = []byte{
	0x00, 0x00, 0x00, 0x20, // size (placeholder)
	0x66, 0x74, 0x79, 0x70, // "ftyp"
	0x69, 0x73, 0x6f, 0x6d, // "isom"
	0x00, 0x00, 0x02, 0x00,
	0x69, 0x73, 0x6f, 0x6d, 0x69, 0x73, 0x6f, 0x32, 0x61, 0x76, 0x63, 0x31, 0x6d, 0x70, 0x34, 0x31,
}

func TestShowVideoReturnsMetadata(t *testing.T) {
	dir := t.TempDir()
	mp4Path := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(mp4Path, mp4Header, 0o644); err != nil {
		t.Fatal(err)
	}

	ok, out, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op":   "video",
		"path": mp4Path,
	}))
	if !ok || err != nil {
		t.Fatalf("show video failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, `"show"`) {
		t.Errorf("output should contain show key, got: %s", out)
	}
	if !contains(out, `"type":"video"`) {
		t.Errorf("output should set type=video, got: %s", out)
	}
	if !contains(out, `"path":"`+jsonPath(mp4Path)+`"`) {
		t.Errorf("output should include original path, got: %s", out)
	}
	if !contains(out, `"media_type":"video/mp4"`) {
		t.Errorf("output should include media_type, got: %s", out)
	}
	if !contains(out, `"size_bytes"`) {
		t.Errorf("output should include size_bytes, got: %s", out)
	}
	// The base64 data URL must NOT be in the output — a 4.5MB video
	// becomes a 6MB base64 string that bloats the conversation JSON and
	// is useless to the LLM. The frontend loads the video via
	// /local-file?path= using the path field.
	if contains(out, "data:video/mp4;base64,") {
		t.Errorf("output must NOT contain base64 data URL, got: %s", out)
	}
}

func TestShowVideoRejectsNonVideoFile(t *testing.T) {
	dir := t.TempDir()
	notVideo := filepath.Join(dir, "not_video.txt")
	// PNG magic bytes — not a video file.
	if err := os.WriteFile(notVideo, []byte{0x89, 0x50, 0x4e, 0x47}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op":   "video",
		"path": notVideo,
	}))
	if err == nil {
		t.Error("non-video file should error")
	}
}

func TestShowVideoSizeCap(t *testing.T) {
	dir := t.TempDir()
	bigPath := filepath.Join(dir, "huge.mp4")
	// Valid header followed by junk past the cap. We only need SniffMagic to
	// recognize the file; the size check fires before the body is read.
	big := append([]byte{}, mp4Header...)
	big = append(big, make([]byte, showMaxVideoBytes+1)...)
	if err := os.WriteFile(bigPath, big, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeFileTool("show", mustJSONArgs(t, map[string]any{
		"op":   "video",
		"path": bigPath,
	}))
	if err == nil {
		t.Error("oversized video should error")
	}
}
