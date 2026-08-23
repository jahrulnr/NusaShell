package tools

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var testTB = &Toolbox{}

func TestFileWriteReadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "hello.txt")

	if _, err := testTB.Execute(context.Background(), "file_write", []byte(`{"path":"`+jsonPath(path)+`","content":"hello world"}`)); err != nil {
		t.Fatalf("file_write: %v", err)
	}
	out, err := testTB.Execute(context.Background(), "file_read", []byte(`{"path":"`+jsonPath(path)+`"}`))
	if err != nil {
		t.Fatalf("file_read: %v", err)
	}
	if !strings.Contains(out, "hello world") {
		t.Fatalf("read output missing content: %q", out)
	}
	// Atomic write must not leave temp files behind.
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".nusashell-tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestFileAppendAndPatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.txt")
	jp := jsonPath(path)

	if _, err := testTB.Execute(context.Background(), "file_append", []byte(`{"path":"`+jp+`","content":"line1\n"}`)); err != nil {
		t.Fatalf("file_append: %v", err)
	}
	if _, err := testTB.Execute(context.Background(), "file_append", []byte(`{"path":"`+jp+`","content":"line2\n"}`)); err != nil {
		t.Fatalf("file_append 2: %v", err)
	}

	// Unique replace.
	if _, err := testTB.Execute(context.Background(), "file_patch", []byte(`{"path":"`+jp+`","old_string":"line1","new_string":"LINE1"}`)); err != nil {
		t.Fatalf("file_patch: %v", err)
	}
	out, _ := testTB.Execute(context.Background(), "file_read", []byte(`{"path":"`+jp+`"}`))
	if !strings.Contains(out, "LINE1") || strings.Contains(out, "line1") {
		t.Fatalf("patch not applied: %q", out)
	}

	// Ambiguous replace requires occurrence.
	testTB.Execute(context.Background(), "file_append", []byte(`{"path":"`+jp+`","content":"dup dup\n"}`))
	if _, err := testTB.Execute(context.Background(), "file_patch", []byte(`{"path":"`+jp+`","old_string":"dup","new_string":"x"}`)); err == nil || !strings.Contains(err.Error(), "occurrence") {
		t.Fatalf("expected occurrence error, got: %v", err)
	}
	if _, err := testTB.Execute(context.Background(), "file_patch", []byte(`{"path":"`+jp+`","old_string":"dup","new_string":"x","occurrence":2}`)); err != nil {
		t.Fatalf("file_patch occurrence=2: %v", err)
	}
	out, _ = testTB.Execute(context.Background(), "file_read", []byte(`{"path":"`+jp+`"}`))
	if !strings.Contains(out, "dup x") {
		t.Fatalf("occurrence=2 not honored: %q", out)
	}

	// Preview must not write.
	before, _ := os.ReadFile(path)
	prev, err := testTB.Execute(context.Background(), "file_patch", []byte(`{"path":"`+jp+`","old_string":"LINE1","new_string":"nope","preview":true}`))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !strings.Contains(prev, "nope") {
		t.Fatalf("preview missing result: %q", prev)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatalf("preview modified the file")
	}
}

func TestFileListMkdirExistsDelete(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a", "b")
	jp := jsonPath(sub)

	if _, err := testTB.Execute(context.Background(), "file_mkdir", []byte(`{"path":"`+jp+`"}`)); err != nil {
		t.Fatalf("file_mkdir: %v", err)
	}
	out, err := testTB.Execute(context.Background(), "file_exists", []byte(`{"path":"`+jp+`"}`))
	if err != nil || !strings.Contains(out, "exists: true") {
		t.Fatalf("file_exists: %v %q", err, out)
	}

	if _, err := testTB.Execute(context.Background(), "file_write", []byte(`{"path":"`+jp+`/f.txt","content":"x"}`)); err != nil {
		t.Fatalf("file_write: %v", err)
	}
	// Non-empty dir without recursive must fail.
	if _, err := testTB.Execute(context.Background(), "file_delete", []byte(`{"path":"`+jp+`"}`)); err == nil {
		t.Fatalf("expected error deleting non-empty dir")
	}
	if _, err := testTB.Execute(context.Background(), "file_delete", []byte(`{"path":"`+jp+`","recursive":true}`)); err != nil {
		t.Fatalf("file_delete recursive: %v", err)
	}
	out, _ = testTB.Execute(context.Background(), "file_exists", []byte(`{"path":"`+jp+`"}`))
	if !strings.Contains(out, "exists: false") {
		t.Fatalf("dir still exists: %q", out)
	}

	// Missing path must not error.
	if _, err := testTB.Execute(context.Background(), "file_exists", []byte(`{"path":"`+jsonPath(filepath.Join(dir, "nope"))+`"}`)); err != nil {
		t.Fatalf("file_exists on missing path errored: %v", err)
	}
}

func TestFileMoveCopyInfoList(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	jp := jsonPath(src)

	testTB.Execute(context.Background(), "file_write", []byte(`{"path":"`+jp+`","content":"payload"}`))
	if _, err := testTB.Execute(context.Background(), "file_move", []byte(`{"source":"`+jp+`","destination":"`+jsonPath(filepath.Join(dir, "renamed.txt"))+`"}`)); err != nil {
		t.Fatalf("file_move: %v", err)
	}
	if _, err := testTB.Execute(context.Background(), "file_copy", []byte(`{"source":"`+jsonPath(filepath.Join(dir, "renamed.txt"))+`","destination":"`+jsonPath(filepath.Join(dir, "copied.txt"))+`"}`)); err != nil {
		t.Fatalf("file_copy: %v", err)
	}
	out, _ := testTB.Execute(context.Background(), "file_list", []byte(`{"path":"`+jsonPath(dir)+`"}`))
	if !strings.Contains(out, "renamed.txt") || !strings.Contains(out, "copied.txt") {
		t.Fatalf("file_list missing entries: %q", out)
	}
	info, err := testTB.Execute(context.Background(), "file_info", []byte(`{"path":"`+jsonPath(filepath.Join(dir, "copied.txt"))+`"}`))
	if err != nil || !strings.Contains(info, "size: 7") {
		t.Fatalf("file_info: %v %q", err, info)
	}
}

func TestFileReadBinaryAndTruncation(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "blob.bin")
	// 0x00 in the first 1KB marks binary.
	if _, err := testTB.Execute(context.Background(), "file_write", []byte(`{"path":"`+jsonPath(bin)+`","encoding":"base64","content":"`+base64.StdEncoding.EncodeToString([]byte{0x00, 0x01, 0x02})+`"}`)); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	out, err := testTB.Execute(context.Background(), "file_read", []byte(`{"path":"`+jsonPath(bin)+`"}`))
	if err != nil || !strings.Contains(out, "binary: true") {
		t.Fatalf("binary not detected: %v %q", err, out)
	}

	big := filepath.Join(dir, "big.txt")
	content := strings.Repeat("a", 100)
	testTB.Execute(context.Background(), "file_write", []byte(`{"path":"`+jsonPath(big)+`","content":"`+content+`"}`))
	out, err = testTB.Execute(context.Background(), "file_read", []byte(`{"path":"`+jsonPath(big)+`","max_bytes":10}`))
	if err != nil {
		t.Fatalf("read big: %v", err)
	}
	if !strings.Contains(out, "truncated: true") || !strings.Contains(out, "next_offset: 10") {
		t.Fatalf("truncation metadata missing: %q", out)
	}
}

func TestFileToolInfosRegistered(t *testing.T) {
	tb := &Toolbox{}
	names := map[string]bool{}
	for _, ti := range tb.ListTools() {
		names[ti.Name] = true
	}
	for _, want := range []string{"file_read", "file_write", "file_append", "file_patch", "file_list", "file_mkdir", "file_delete", "file_move", "file_copy", "file_exists", "file_info", "exec"} {
		if !names[want] {
			t.Errorf("ListTools missing %q", want)
		}
	}
}

// jsonPath escapes a path for embedding in a JSON string literal.
func jsonPath(p string) string {
	r := strings.NewReplacer("\\", "\\\\", "\"", "\\\"")
	return r.Replace(p)
}
