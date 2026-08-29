package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"nusashell/application"
)

func TestCapToolOutputSmallUnchanged(t *testing.T) {
	out := capToolOutput("grep", map[string]any{"via": "rg"}, "hello")
	if strings.Contains(out, "overflow_path") || strings.Contains(out, "truncated: true") {
		t.Fatalf("small body should not spill: %s", out)
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "via: rg") {
		t.Fatalf("missing content: %s", out)
	}
}

func TestCapToolOutputSpillsToPlatformTemp(t *testing.T) {
	body := strings.Repeat("x", toolInlineMaxBytes+64)
	out := capToolOutput("web_fetch", map[string]any{"status": 200}, body)
	if !strings.Contains(out, "truncated: true") {
		t.Fatalf("expected truncated flag: %s", out[:200])
	}
	if !strings.Contains(out, "next_offset_bytes:") {
		t.Fatalf("expected next_offset_bytes for file_read: %s", out[:400])
	}
	path := overflowPathFrom(t, out)
	wantDir := filepath.Join(os.TempDir(), toolOverflowDirName)
	if filepath.Dir(path) != wantDir {
		t.Fatalf("overflow dir = %q, want platform temp %q", filepath.Dir(path), wantDir)
	}
	if !strings.HasPrefix(filepath.Base(path), "web_fetch-") || !strings.HasSuffix(path, ".txt") {
		t.Fatalf("unexpected filename: %s", path)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	if string(saved) != body {
		t.Fatalf("spill file len=%d, want %d", len(saved), len(body))
	}
	if strings.Count(out, "x") >= len(body) {
		t.Fatal("in-band result still contains the full body")
	}
	if !strings.Contains(out, "overflow_bytes: "+itoa(len(body))) && !strings.Contains(out, "overflow_bytes:") {
		t.Fatalf("missing overflow_bytes: %s", out[:400])
	}
}

func TestClipUTF8PrefixDoesNotSplitRune(t *testing.T) {
	s := "ééé" // each é is 2 bytes
	got := clipUTF8Prefix(s, 3)
	if !utf8.ValidString(got) {
		t.Fatalf("invalid utf8: %q", got)
	}
}

func TestWriteToolOverflowCrossPlatformName(t *testing.T) {
	path, err := writeToolOverflow("docs", "payload")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	if !filepath.IsAbs(path) {
		t.Fatalf("path must be absolute for file_read: %s", path)
	}
	if filepath.Separator == '\\' && !strings.Contains(path, `\`) && !strings.Contains(path, `nusashell`) {
		t.Fatalf("windows path should use platform separators: %s", path)
	}
}

func overflowPathFrom(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "overflow_path:") {
			p := strings.TrimSpace(strings.TrimPrefix(line, "overflow_path:"))
			p = strings.Trim(p, `"'`)
			if p == "" {
				t.Fatalf("empty overflow_path in: %s", out[:400])
			}
			return p
		}
	}
	t.Fatalf("overflow_path missing in: %s", out[:500])
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [32]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

type stubDocsOverflow struct{ content string }

func (s stubDocsOverflow) List() []application.DocMeta             { return nil }
func (s stubDocsOverflow) Search(string, int) []application.DocHit { return nil }
func (s stubDocsOverflow) Read(id string) (application.DocFull, error) {
	return application.DocFull{
		DocMeta: application.DocMeta{ID: id, Title: "Big", Path: "big.md"},
		Content: s.content,
	}, nil
}

func TestDocsReadSpillsOversizedPage(t *testing.T) {
	body := strings.Repeat("doc-line\n", 6000)
	tb := &Toolbox{Docs: stubDocsOverflow{content: body}}
	out, err := tb.Execute(context.Background(), "docs", []byte(`{"op":"read","id":"big"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "overflow_path:") {
		t.Fatalf("expected spill: %s", out[:min(len(out), 400)])
	}
	path := overflowPathFrom(t, out)
	t.Cleanup(func() { _ = os.Remove(path) })
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != body {
		t.Fatalf("spill mismatch len=%d want=%d", len(saved), len(body))
	}
}

func TestCapJSONLSpills(t *testing.T) {
	items := make([]any, 0, 200)
	for i := 0; i < 200; i++ {
		items = append(items, map[string]any{"title": strings.Repeat("t", 200), "url": "https://example.com", "snippet": strings.Repeat("s", 200)})
	}
	out := capJSONL("web_search", map[string]any{"count": len(items)}, items)
	if !strings.Contains(out, "overflow_path:") {
		t.Fatalf("expected jsonl spill: %s", out[:min(len(out), 300)])
	}
	path := overflowPathFrom(t, out)
	t.Cleanup(func() { _ = os.Remove(path) })
}

func TestSweepOverflowRemovesOnlyAgedFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	oldPath := filepath.Join(dir, "grep-old.txt")
	freshPath := filepath.Join(dir, "grep-fresh.txt")
	keepDir := filepath.Join(dir, "subdir")
	if err := os.WriteFile(oldPath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(freshPath, []byte("fresh"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(keepDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldTime := now.Add(-ToolOverflowMaxAge - time.Second)
	freshTime := now.Add(-23 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(freshPath, freshTime, freshTime); err != nil {
		t.Fatal(err)
	}

	n, err := sweepToolOverflowDir(dir, now, ToolOverflowMaxAge)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("removed %d, want 1", n)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatal("aged file should be removed")
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Fatalf("fresh file should remain: %v", err)
	}
	if _, err := os.Stat(keepDir); err != nil {
		t.Fatalf("subdir should remain: %v", err)
	}
}

func TestSweepOverflowMissingDir(t *testing.T) {
	n, err := sweepToolOverflowDir(filepath.Join(t.TempDir(), "nope"), time.Now(), ToolOverflowMaxAge)
	if err != nil || n != 0 {
		t.Fatalf("missing dir: n=%d err=%v", n, err)
	}
}

func TestRunOverflowCleanupStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runOverflowCleanup(ctx, ToolOverflowMaxAge, 20*time.Millisecond, time.Now)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup loop did not exit after cancel")
	}
}
