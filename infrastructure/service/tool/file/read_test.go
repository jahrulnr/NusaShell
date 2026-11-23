package toolfile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/service"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
)

func TestFileReadToolReads(t *testing.T) {
	root := t.TempDir()
	tool := mustReadTool(t, root)
	if tool.Schema().Name() != "read_file" {
		t.Fatalf("schema name = %q", tool.Schema().Name())
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runRead(t, tool, `{"path":"a.txt"}`)
	if err != nil || out != "hello" {
		t.Fatalf("read = %q, %v", out, err)
	}
}

func TestFileReadToolRejects(t *testing.T) {
	root := t.TempDir()
	tool := mustReadTool(t, root)
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]string{
		"escape":       `{"path":"../evil.txt"}`,
		"dir path":     `{"path":"sub"}`,
		"missing file": `{"path":"none.txt"}`,
		"bad json":     `{oops`,
	} {
		if _, err := runRead(t, tool, input); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

// The schema promises UTF-8 text, but the tool streams whatever bytes
// the confined open yields — a small binary file is returned verbatim
// into model context instead of being rejected with a redirect to
// read_media, the same check read_media applies in reverse.
func TestFileReadRejectsBinaryContent(t *testing.T) {
	root := t.TempDir()
	tool := mustReadTool(t, root)
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	if err := os.WriteFile(filepath.Join(root, "pic.png"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runRead(t, tool, `{"path":"pic.png"}`)
	if err == nil {
		t.Fatal("binary content returned as text — must redirect to read_media")
	}
	if !strings.Contains(err.Error(), "read_media") {
		t.Fatalf("binary read error must point at read_media: %v", err)
	}
}

// Text-sniffed bytes that are not valid UTF-8 (Latin-1 here) used to
// pass the MIME gate and reach the model as U+FFFD replacement
// characters — a corrupted document with no warning. The tool must
// reject with a conversion hint instead.
func TestFileReadRejectsNonUTF8Text(t *testing.T) {
	root := t.TempDir()
	tool := mustReadTool(t, root)
	if err := os.WriteFile(filepath.Join(root, "latin.txt"), []byte("caf\xe9"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runRead(t, tool, `{"path":"latin.txt"}`)
	if err == nil {
		t.Fatal("Latin-1 content returned as UTF-8 text")
	}
	if !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("non-UTF-8 read error must name the encoding problem: %v", err)
	}
}

func mustReadTool(t *testing.T, root string) service.Tool {
	t.Helper()
	tool, err := NewFileReadTool(repoworkspace.NewLocal(), root)
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func runRead(t *testing.T, tool service.Tool, input string) (string, error) {
	t.Helper()
	tc, err := entity.NewToolCall("call_1", "read_file", input, enum.TooltypeOrigin)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), tc, nil)
	return res.Text, err
}

// A file longer than one answer comes back a page at a time, whole lines
// only, each page saying where to continue.
func TestFileReadPages(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&b, "line %03d %s\n", i, strings.Repeat("x", 90))
	}
	text := b.String() // 100 lines of 100 bytes
	page, err := readPage(text, 1, 0, 2_000)
	if err != nil || !strings.HasPrefix(page, "line 001") || !strings.Contains(page, "[read_file: lines 1-18 of 100. Continue with offset=19]") {
		t.Fatalf("first page: %v\n%s", err, page)
	}
	page, _ = readPage(text, 19, 0, 2_000)
	if !strings.HasPrefix(page, "line 019") {
		t.Fatalf("second page starts at %q", page[:8])
	}
	page, _ = readPage(text, 98, 0, 2_000)
	if !strings.Contains(page, "lines 98-100 of 100]") || strings.Contains(page, "Continue") {
		t.Fatalf("last page: %s", page)
	}
	page, _ = readPage(text, 10, 2, 2_000)
	if !strings.HasPrefix(page, "line 010") || !strings.Contains(page, "lines 10-11 of 100. Continue with offset=12]") {
		t.Fatalf("limit: %s", page)
	}
	if got, _ := readPage("a\nb\n", 1, 0, 2_000); got != "a\nb\n" {
		t.Fatalf("a whole file answers as it is: %q", got)
	}
	if _, err := readPage(text, 101, 0, 2_000); err == nil {
		t.Fatal("an offset past the end must say so")
	}
	long := strings.Repeat("é", 3_000) + "\nnext\n"
	page, _ = readPage(long, 1, 0, 1_000)
	if !utf8.ValidString(page) || !strings.Contains(page, "line 1 is cut") || !strings.Contains(page, "offset=2") {
		t.Fatalf("a line longer than a page: %s", page[len(page)-80:])
	}
}

// read_file pages a large file instead of refusing it.
func TestFileReadToolPagesLargeFile(t *testing.T) {
	root := t.TempDir()
	tool := mustReadTool(t, root)
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(strings.Repeat("0123456789\n", 10_000)), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runRead(t, tool, `{"path":"big.txt","offset":5000,"limit":3}`)
	if err != nil || out != "0123456789\n0123456789\n0123456789\n\n[read_file: lines 5000-5002 of 10000. Continue with offset=5003]" {
		t.Fatalf("read = %q, %v", out, err)
	}
	out, err = runRead(t, tool, `{"path":"big.txt"}`)
	if err != nil || len(out) > 32_000 || !strings.Contains(out, "Continue with offset=") {
		t.Fatalf("default page: %d bytes, %v", len(out), err)
	}
}
