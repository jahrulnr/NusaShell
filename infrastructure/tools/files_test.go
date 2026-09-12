package tools

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
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

func TestFileWriteAndPatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.txt")
	jp := jsonPath(path)

	if _, err := testTB.Execute(context.Background(), "file_write", []byte(`{"path":"`+jp+`","content":"line1\nline2\n"}`)); err != nil {
		t.Fatalf("file_write: %v", err)
	}

	// Unique replace.
	if _, err := testTB.Execute(context.Background(), "file_patch", []byte(`{"path":"`+jp+`","old_string":"line1","new_string":"LINE1"}`)); err != nil {
		t.Fatalf("file_patch: %v", err)
	}
	out, _ := testTB.Execute(context.Background(), "file_read", []byte(`{"path":"`+jp+`"}`))
	if !strings.Contains(out, "LINE1") || strings.Contains(out, "line1") {
		t.Fatalf("patch not applied: %q", out)
	}

	// Ambiguous replace requires occurrence — write a file with duplicates.
	dupPath := filepath.Join(dir, "dups.txt")
	jdp := jsonPath(dupPath)
	if _, err := testTB.Execute(context.Background(), "file_write", []byte(`{"path":"`+jdp+`","content":"dup dup\n"}`)); err != nil {
		t.Fatalf("file_write dups: %v", err)
	}
	if _, err := testTB.Execute(context.Background(), "file_patch", []byte(`{"path":"`+jdp+`","old_string":"dup","new_string":"x"}`)); err == nil || !strings.Contains(err.Error(), "occurrence") {
		t.Fatalf("expected occurrence error, got: %v", err)
	}
	if _, err := testTB.Execute(context.Background(), "file_patch", []byte(`{"path":"`+jdp+`","old_string":"dup","new_string":"x","occurrence":2}`)); err != nil {
		t.Fatalf("file_patch occurrence=2: %v", err)
	}
	out, _ = testTB.Execute(context.Background(), "file_read", []byte(`{"path":"`+jdp+`"}`))
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

func TestFileReadAndPatchExposeContentVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "versioned.txt")
	content := "alpha\nbeta\n"

	if _, err := testTB.Execute(context.Background(), "file_write", fileJSON(map[string]any{"path": path, "content": content})); err != nil {
		t.Fatalf("write: %v", err)
	}
	wantBefore := sha256.Sum256([]byte(content))
	beforeHash := hex.EncodeToString(wantBefore[:])

	readOut, err := testTB.Execute(context.Background(), "file_read", []byte(`{"path":"`+jsonPath(path)+`"}`))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(readOut, "sha256: "+beforeHash) {
		t.Fatalf("file_read must expose the content version: %q", readOut)
	}

	patchOut, err := testTB.Execute(context.Background(), "file_patch", fileJSON(map[string]any{"path": path, "old_string": "beta", "new_string": "BETA"}))
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	wantAfter := sha256.Sum256([]byte("alpha\nBETA\n"))
	if !strings.Contains(patchOut, "sha256: "+hex.EncodeToString(wantAfter[:])) {
		t.Fatalf("file_patch must expose the new content version: %q", patchOut)
	}
}

func TestFilePatchFailureExplainsStaleContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "layout.css")
	content := ".sidebar {\n  width: 240px;\n}\n"
	if _, err := testTB.Execute(context.Background(), "file_write", fileJSON(map[string]any{"path": path, "content": content})); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := testTB.Execute(context.Background(), "file_patch", fileJSON(map[string]any{
		"path":       path,
		"old_string": ".sidebar {\n  width: 230px;\n}",
		"new_string": ".sidebar {\n  width: 280px;\n}",
	}))
	if err == nil {
		t.Fatal("expected stale-context error")
	}
	for _, want := range []string{
		"PATCH_CONTEXT_NOT_FOUND",
		"current_sha256=",
		"old_string_bytes=",
		"re-read",
		"encoding=escaped",
		"nearby_context",
		"line 1:",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("stale-context error missing %q: %v", want, err)
		}
	}
}

// TestFilePatchIgnoresLegacyExpectedSha256: the expected_sha256
// fail-closed guard was removed so disciplined models can work in parallel
// with other agents (another agent editing the file mid-flight no longer
// blocks this patch). The tool no longer advertises the parameter, and a
// call that still passes it (older agents, cached tool schemas) must be
// ignored, not rejected or failed.
func TestFilePatchIgnoresLegacyExpectedSha256(t *testing.T) {
	// The advertised schema must not carry the removed parameter.
	payload, err := json.Marshal(fileToolInfos()[2].InputSchema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	if strings.Contains(string(payload), "expected_sha256") {
		t.Fatal("file_patch schema must not advertise expected_sha256")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "versioned.txt")
	if _, err := testTB.Execute(context.Background(), "file_write", fileJSON(map[string]any{"path": path, "content": "one\ntwo\n"})); err != nil {
		t.Fatalf("write: %v", err)
	}

	// A stale/wrong hash passed by an older client must be ignored: the
	// patch applies normally instead of failing FILE_CHANGED_SINCE_READ.
	_, err = testTB.Execute(context.Background(), "file_patch", fileJSON(map[string]any{
		"path": path, "old_string": "two", "new_string": "TWO",
		"expected_sha256": "0000000000000000000000000000000000000000000000000000000000000000",
	}))
	if err != nil {
		t.Fatalf("legacy expected_sha256 must be ignored, got: %v", err)
	}
	out, readErr := testTB.Execute(context.Background(), "file_read", []byte(`{"path":"`+jsonPath(path)+`"}`))
	if readErr != nil || !strings.Contains(out, "TWO") {
		t.Fatalf("patch must have been applied despite stale param: err=%v out=%q", readErr, out)
	}
}

func TestFileToolsPreserveAndShowInvisibleWhitespace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "styles.css")
	escaped := "a {\\r\\n\\tcolor: red;  \\r\\n}\\r\\n"

	writeOut, err := testTB.Execute(context.Background(), "file_write", fileJSON(map[string]any{
		"path": path, "content": escaped, "encoding": "escaped",
	}))
	if err != nil {
		t.Fatalf("escaped file_write: %v", err)
	}
	for _, want := range []string{"line_ending: crlf", "tabs: 1", "carriage_returns: 3", "trailing_whitespace_lines: 1"} {
		if !strings.Contains(writeOut, want) {
			t.Errorf("file_write metadata missing %q: %s", want, writeOut)
		}
	}

	wantRaw := []byte("a {\r\n\tcolor: red;  \r\n}\r\n")
	gotRaw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read raw file: %v", err)
	}
	if string(gotRaw) != string(wantRaw) {
		t.Fatalf("escaped file_write changed bytes: got %q want %q", gotRaw, wantRaw)
	}

	readOut, err := testTB.Execute(context.Background(), "file_read", fileJSON(map[string]any{
		"path": path, "show_whitespace": true,
	}))
	if err != nil {
		t.Fatalf("visible file_read: %v", err)
	}
	for _, want := range []string{`\r`, `\tcolor: red`, "line_ending: crlf", "tabs: 1"} {
		if !strings.Contains(readOut, want) {
			t.Errorf("visible file_read missing %q: %s", want, readOut)
		}
	}

	patchOut, err := testTB.Execute(context.Background(), "file_patch", fileJSON(map[string]any{
		"path":       path,
		"old_string": escaped,
		"new_string": "a {\\r\\n\\tcolor: blue;\\r\\n}\\r\\n",
		"encoding":   "escaped",
	}))
	if err != nil {
		t.Fatalf("escaped file_patch: %v", err)
	}
	for _, want := range []string{"line_ending: crlf", "tabs: 1", "carriage_returns: 3", "trailing_whitespace_lines: 0"} {
		if !strings.Contains(patchOut, want) {
			t.Errorf("file_patch metadata missing %q: %s", want, patchOut)
		}
	}
	wantPatched := []byte("a {\r\n\tcolor: blue;\r\n}\r\n")
	gotPatched, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if string(gotPatched) != string(wantPatched) {
		t.Fatalf("escaped file_patch changed bytes: got %q want %q", gotPatched, wantPatched)
	}
}

func TestFilePatchAutoHealsUniqueWhitespaceMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "styles.css")
	content := "a {\r\n\tcolor: red;\r\n}\r\n"
	if _, err := testTB.Execute(context.Background(), "file_write", fileJSON(map[string]any{
		"path": path, "content": content,
	})); err != nil {
		t.Fatalf("write: %v", err)
	}

	patchOut, err := testTB.Execute(context.Background(), "file_patch", fileJSON(map[string]any{
		"path":       path,
		"old_string": "a {\n  color: red;\n}",
		"new_string": "a {\n  color: blue;\n}",
	}))
	if err != nil {
		t.Fatalf("unique whitespace mismatch should auto-heal: %v", err)
	}
	for _, want := range []string{"healed: true", "match_mode: whitespace", "line_ending: crlf"} {
		if !strings.Contains(patchOut, want) {
			t.Errorf("auto-heal result missing %q: %s", want, patchOut)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	want := "a {\r\n  color: blue;\r\n}\r\n"
	if string(got) != want {
		t.Fatalf("auto-heal introduced mixed line endings: got %q want %q", got, want)
	}
}

func TestFilePatchAutoHealRejectsAmbiguousWhitespaceMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "values.txt")
	content := "key\tvalue\r\nkey  value\r\n"
	if _, err := testTB.Execute(context.Background(), "file_write", fileJSON(map[string]any{
		"path": path, "content": content,
	})); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := testTB.Execute(context.Background(), "file_patch", fileJSON(map[string]any{
		"path": path, "old_string": "key value", "new_string": "key changed",
	}))
	if err == nil || !strings.Contains(err.Error(), "PATCH_CONTEXT_AMBIGUOUS") {
		t.Fatalf("expected ambiguous whitespace error, got %v", err)
	}
	if !strings.Contains(err.Error(), "candidate_lines=[1 2]") {
		t.Fatalf("ambiguous error should list candidate line numbers, got %v", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != content {
		t.Fatalf("ambiguous auto-heal must not write: err=%v got=%q", readErr, got)
	}
}

func TestFilePatchWhitespaceFailureShowsVisibleContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "styles.css")
	content := "a {\\r\\n\\tcolor: red;\\r\\n}\\r\\n"
	if _, err := testTB.Execute(context.Background(), "file_write", fileJSON(map[string]any{
		"path": path, "content": content, "encoding": "escaped",
	})); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := testTB.Execute(context.Background(), "file_patch", fileJSON(map[string]any{
		"path":       path,
		"old_string": "a {\n  color: red;\n}",
		"new_string": "a {\n  color: blue;\n}",
		"auto_heal":  false,
	}))
	if err == nil {
		t.Fatal("expected whitespace-sensitive patch error")
	}
	for _, want := range []string{
		"PATCH_CONTEXT_NOT_FOUND",
		"line_ending=crlf",
		"tabs=1",
		"carriage_returns=3",
		`\r`,
		`\t`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("whitespace diagnostic missing %q: %v", want, err)
		}
	}
}

func TestFileListMkdirExistsDelete(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a", "b")
	jp := jsonPath(sub)

	if _, err := testTB.Execute(context.Background(), "file_mkdir", []byte(`{"path":"`+jp+`"}`)); err != nil {
		t.Fatalf("file_mkdir: %v", err)
	}
	out, err := testTB.Execute(context.Background(), "file_info", []byte(`{"path":"`+jp+`"}`))
	if err != nil || !strings.Contains(out, "exists: true") {
		t.Fatalf("file_info: %v %q", err, out)
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
	out, _ = testTB.Execute(context.Background(), "file_info", []byte(`{"path":"`+jp+`"}`))
	if !strings.Contains(out, "exists: false") {
		t.Fatalf("dir still exists: %q", out)
	}

	// Missing path must not error.
	if _, err := testTB.Execute(context.Background(), "file_info", []byte(`{"path":"`+jsonPath(filepath.Join(dir, "nope"))+`"}`)); err != nil {
		t.Fatalf("file_info on missing path errored: %v", err)
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
	if !strings.Contains(out, "truncated: true") || !strings.Contains(out, "next_offset_bytes: 10") {
		t.Fatalf("truncation metadata missing: %q", out)
	}
}

// Self-describing I/O: every numeric coordinate file_read reports carries
// its unit in the field name, so an agent never has to remember whether a
// bare integer is a byte offset or a line number (audit conv_c21e02199596a3cc:
// offset/line confusion lands reads hundreds of lines off).
// Graded read budget: a blind whole-file read of a large file must not park a
// 32KiB head in the transcript. That head taxes the attention budget of every
// later round (context rot) and can anchor the model on a partial prefix.
// Explicit targeting (max_bytes, start_line/end_line, offset_bytes) opts out.
func TestFileReadLargeFileGetsGradedHead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.log")
	content := strings.Repeat("log line\n", (fileReadLargeTierBytes/9)+2048)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	out, err := testTB.Execute(context.Background(), "file_read", fileJSON(map[string]any{"path": path}))
	if err != nil {
		t.Fatalf("file_read: %v", err)
	}
	head := out[:min(len(out), 400)]
	if !strings.Contains(out, fmt.Sprintf("file_bytes: %d", len(content))) {
		t.Errorf("large read must report the complete file size: %s", head)
	}
	if !strings.Contains(out, fmt.Sprintf("\nbytes: %d\n", fileReadLargeMaxBytes)) {
		t.Errorf("large read head must shrink to the graded budget: %s", head)
	}
	if !strings.Contains(out, fmt.Sprintf("next_offset_bytes: %d", fileReadLargeMaxBytes)) {
		t.Errorf("graded head must stay addressable: %s", head)
	}
	if !strings.Contains(out, "truncated: true") || !strings.Contains(out, "hint:") {
		t.Errorf("graded head must carry truncation + a targeting hint: %s", head)
	}
	if len(out) > fileReadLargeMaxBytes+2048 {
		t.Errorf("in-band payload still too large: %d bytes", len(out))
	}
}

func TestFileReadHugeFileIsMetadataOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.log")
	content := strings.Repeat("log line\n", (fileReadHugeTierBytes/9)+1)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	out, err := testTB.Execute(context.Background(), "file_read", fileJSON(map[string]any{"path": path}))
	if err != nil {
		t.Fatalf("file_read: %v", err)
	}
	head := out[:min(len(out), 400)]
	if !strings.Contains(out, "\nbytes: 0\n") {
		t.Errorf("huge file should return metadata only: %s", head)
	}
	if !strings.Contains(out, fmt.Sprintf("file_bytes: %d", len(content))) || !strings.Contains(out, "truncated: true") {
		t.Errorf("stub must report the complete size and truncation: %s", head)
	}
	if !strings.Contains(out, "hint:") {
		t.Errorf("stub must explain how to read a slice: %s", head)
	}
	if strings.Contains(out, "log line") {
		t.Errorf("stub must not carry file content: %s", head)
	}
	// Explicit line targeting opts out of grading.
	out, err = testTB.Execute(context.Background(), "file_read", fileJSON(map[string]any{"path": path, "start_line": 1, "end_line": 2}))
	if err != nil {
		t.Fatalf("line read: %v", err)
	}
	if !strings.Contains(out, "log line") {
		t.Errorf("line-targeted read must return content: %s", out[:min(len(out), 400)])
	}
	// An explicit byte budget is an opt-in too.
	out, err = testTB.Execute(context.Background(), "file_read", fileJSON(map[string]any{"path": path, "max_bytes": 8192}))
	if err != nil {
		t.Fatalf("explicit max_bytes read: %v", err)
	}
	if !strings.Contains(out, "\nbytes: 8192\n") {
		t.Errorf("explicit max_bytes must be honored: %s", out[:min(len(out), 400)])
	}
}

func TestFileReadSmallFileKeepsFullBody(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.txt")
	content := "alpha\nbeta\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	out, err := testTB.Execute(context.Background(), "file_read", fileJSON(map[string]any{"path": path}))
	if err != nil {
		t.Fatalf("file_read: %v", err)
	}
	for _, want := range []string{"alpha\nbeta", fmt.Sprintf("file_bytes: %d", len(content))} {
		if !strings.Contains(out, want) {
			t.Errorf("small read missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "truncated") || strings.Contains(out, "hint:") {
		t.Errorf("small read must not be graded or hinted: %s", out)
	}
}

func TestFileReadSelfDescribingOffsets(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.txt")
	content := strings.Repeat("a", 100)
	if _, err := testTB.Execute(context.Background(), "file_write", []byte(`{"path":"`+jsonPath(big)+`","content":"`+content+`"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	out, err := testTB.Execute(context.Background(), "file_read", []byte(`{"path":"`+jsonPath(big)+`","offset_bytes":10,"max_bytes":10}`))
	if err != nil {
		t.Fatalf("read with offset_bytes: %v", err)
	}
	if !strings.Contains(out, "offset_bytes: 10") {
		t.Errorf("result must echo the unit-labeled offset: %q", out)
	}
	if !strings.Contains(out, "next_offset_bytes: 20") {
		t.Errorf("truncated result must carry next_offset_bytes: %q", out)
	}
	// No unit-less coordinate may leak into the output.
	for _, bare := range []string{"\noffset:", "\nnext_offset:"} {
		if strings.Contains(out, bare) {
			t.Errorf("unit-less field %q leaked into output: %q", bare, out)
		}
	}
}

// Line mode lets the model read by 1-based line numbers (as grep reports
// them) instead of computing byte offsets, which models routinely confuse
// (audit conv: offset/line confusion lands reads hundreds of lines off).
func TestFileReadLineRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lines.txt")
	content := "one\ntwo\nthree\nfour\nfive\n"
	if _, err := testTB.Execute(context.Background(), "file_write", fileJSON(map[string]any{"path": path, "content": content})); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Middle range, inclusive.
	out, err := testTB.Execute(context.Background(), "file_read", fileJSON(map[string]any{"path": path, "start_line": 2, "end_line": 4}))
	if err != nil {
		t.Fatalf("read lines 2-4: %v", err)
	}
	for _, want := range []string{"start_line: 2", "end_line: 4", "total_lines: 5", "two\nthree\nfour"} {
		if !strings.Contains(out, want) {
			t.Errorf("line range read missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "one") || strings.Contains(out, "five") {
		t.Errorf("line range read leaked outside range: %q", out)
	}

	// end_line beyond EOF clamps to the last line.
	out, err = testTB.Execute(context.Background(), "file_read", fileJSON(map[string]any{"path": path, "start_line": 4, "end_line": 99}))
	if err != nil {
		t.Fatalf("read clamped range: %v", err)
	}
	for _, want := range []string{"start_line: 4", "end_line: 5", "four\nfive"} {
		if !strings.Contains(out, want) {
			t.Errorf("clamped read missing %q: %s", want, out)
		}
	}

	// start_line beyond EOF returns an empty page with the empty range echoed.
	out, err = testTB.Execute(context.Background(), "file_read", fileJSON(map[string]any{"path": path, "start_line": 9}))
	if err != nil {
		t.Fatalf("read beyond EOF: %v", err)
	}
	if !strings.Contains(out, "start_line: 9") || !strings.Contains(out, "end_line: 8") {
		t.Errorf("beyond-EOF read should echo the empty range: %q", out)
	}

	// end_line < start_line is an invalid request.
	if _, err := testTB.Execute(context.Background(), "file_read", fileJSON(map[string]any{"path": path, "start_line": 4, "end_line": 2})); err == nil || !strings.Contains(err.Error(), "end_line") {
		t.Fatalf("expected end_line < start_line error, got %v", err)
	}

	// max_bytes truncation continues in line space via next_start_line.
	out, err = testTB.Execute(context.Background(), "file_read", fileJSON(map[string]any{"path": path, "start_line": 2, "max_bytes": 10}))
	if err != nil {
		t.Fatalf("truncated line read: %v", err)
	}
	if !strings.Contains(out, "truncated: true") || !strings.Contains(out, "next_start_line: 4") {
		t.Errorf("truncated line read must report next_start_line: %q", out)
	}

	// Line mode ignores offset_bytes.
	out, err = testTB.Execute(context.Background(), "file_read", fileJSON(map[string]any{"path": path, "start_line": 2, "end_line": 2, "offset_bytes": 99}))
	if err != nil {
		t.Fatalf("line mode with offset_bytes: %v", err)
	}
	if !strings.Contains(out, "two") || strings.Contains(out, "offset_bytes:") {
		t.Errorf("line mode must ignore offset_bytes: %q", out)
	}
}

func TestFileReadLineCountWithoutTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "noeol.txt")
	if _, err := testTB.Execute(context.Background(), "file_write", fileJSON(map[string]any{"path": path, "content": "one\ntwo\nthree"})); err != nil {
		t.Fatalf("write: %v", err)
	}
	out, err := testTB.Execute(context.Background(), "file_read", fileJSON(map[string]any{"path": path, "start_line": 2, "end_line": 3}))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, want := range []string{"total_lines: 3", "start_line: 2", "end_line: 3", "two\nthree"} {
		if !strings.Contains(out, want) {
			t.Errorf("no-trailing-newline read missing %q: %s", want, out)
		}
	}
}

func TestFileToolInfosRegistered(t *testing.T) {
	tb := &Toolbox{}
	names := map[string]bool{}
	for _, ti := range tb.ListTools() {
		names[ti.Name] = true
	}
	for _, want := range []string{"file_read", "file_write", "file_patch", "file_list", "file_mkdir", "file_delete", "file_move", "file_copy", "file_info", "grep", "find_file", "show", "exec"} {
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

func fileJSON(v map[string]any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestWriteFileAtomicRetriesTransientRenameFailure(t *testing.T) {
	origRename, origSleep, origGoos := renameFn, sleepFn, renameGoos
	defer func() { renameFn, sleepFn, renameGoos = origRename, origSleep, origGoos }()

	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	attempts := 0
	renameFn = func(from, to string) error {
		attempts++
		if attempts < 3 {
			// ERROR_SHARING_VIOLATION: antivirus/indexer briefly holds the temp file.
			return fmt.Errorf("rename %s %s: %w", from, to, syscall.Errno(32))
		}
		return os.Rename(from, to)
	}
	var sleeps []time.Duration
	sleepFn = func(d time.Duration) { sleeps = append(sleeps, d) }
	renameGoos = "windows"

	if err := writeFileAtomic(path, []byte("data"), 0o644); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("rename attempts = %d, want 3", attempts)
	}
	if len(sleeps) != 2 {
		t.Fatalf("backoff sleeps = %d, want 2 (%v)", len(sleeps), sleeps)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "data" {
		t.Fatalf("content after retried write: %v %q", err, data)
	}
}

func TestWriteFileAtomicNoRetryOnPermanentRenameFailure(t *testing.T) {
	origRename, origSleep, origGoos := renameFn, sleepFn, renameGoos
	defer func() { renameFn, sleepFn, renameGoos = origRename, origSleep, origGoos }()

	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	attempts := 0
	renameFn = func(from, to string) error {
		attempts++
		return fmt.Errorf("rename: %w", syscall.Errno(2)) // permanent failure class
	}
	sleepCalls := 0
	sleepFn = func(time.Duration) { sleepCalls++ }
	renameGoos = "windows"

	if err := writeFileAtomic(path, []byte("x"), 0o644); err == nil {
		t.Fatal("expected error from permanently failing rename")
	}
	if attempts != 1 {
		t.Fatalf("rename attempts = %d, want 1 (no retry on permanent errors)", attempts)
	}
	if sleepCalls != 0 {
		t.Fatalf("sleep calls = %d, want 0", sleepCalls)
	}
}

func TestWriteFileAtomicRetryIsBounded(t *testing.T) {
	origRename, origSleep, origGoos := renameFn, sleepFn, renameGoos
	defer func() { renameFn, sleepFn, renameGoos = origRename, origSleep, origGoos }()

	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	attempts := 0
	renameFn = func(from, to string) error {
		attempts++
		return fmt.Errorf("rename: %w", syscall.Errno(32))
	}
	sleepFn = func(time.Duration) {}
	renameGoos = "windows"

	err := writeFileAtomic(path, []byte("x"), 0o644)
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if attempts != renameMaxAttempts {
		t.Fatalf("rename attempts = %d, want bounded %d", attempts, renameMaxAttempts)
	}
}

// Parallel file_patch calls on the same path must apply incrementally
// (serialize RMW) so disjoint hunks all land — not last-writer-wins lost updates.
func TestFilePatchConcurrentSameFileAppliesIncrementally(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "parallel.txt")
	initial := "AAA\nBBB\nCCC\nDDD\nEEE\nFFF\nGGG\nHHH\n"
	if _, err := testTB.Execute(context.Background(), "file_write", fileJSON(map[string]any{
		"path": path, "content": initial,
	})); err != nil {
		t.Fatalf("write: %v", err)
	}

	patches := []struct{ old, new string }{
		{"AAA", "aaa"},
		{"BBB", "bbb"},
		{"CCC", "ccc"},
		{"DDD", "ddd"},
		{"EEE", "eee"},
		{"FFF", "fff"},
		{"GGG", "ggg"},
		{"HHH", "hhh"},
	}
	errs := make([]error, len(patches))
	var ready, done sync.WaitGroup
	ready.Add(1)
	done.Add(len(patches))
	for i, p := range patches {
		i, p := i, p
		go func() {
			defer done.Done()
			ready.Wait()
			_, errs[i] = testTB.Execute(context.Background(), "file_patch", fileJSON(map[string]any{
				"path": path, "old_string": p.old, "new_string": p.new,
			}))
		}()
	}
	ready.Done()
	done.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("patch %d (%s→%s): %v", i, patches[i].old, patches[i].new, err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "aaa\nbbb\nccc\nddd\neee\nfff\nggg\nhhh\n"
	if string(got) != want {
		t.Fatalf("lost update under concurrent same-file patch:\ngot  %q\nwant %q", got, want)
	}
}

// TestFileMoveAndPatchConcurrentNoDeadlock runs file_move(src,dst) and
// file_patch(src) concurrently on the same paths. The multi-path lock must
// serialize them so neither deadlocks and the end state is consistent.
//
// The lock guarantees mutual exclusion but not which goroutine acquires
// first, so the exact dst content is non-deterministic:
//   - move wins: src is moved to dst, then the patch reads a missing src and
//     fails without writing → dst holds the original content.
//   - patch wins: src is patched, then moved to dst → dst holds the patched
//     content.
//
// The invariants we can pin deterministically: no deadlock (completes under a
// bounded timeout), src is always removed, dst always exists, and dst content
// is one of the two valid serialized outcomes (never corrupted).
func TestFileMoveAndPatchConcurrentNoDeadlock(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	if _, err := testTB.Execute(context.Background(), "file_write", fileJSON(map[string]any{
		"path": src, "content": "AAA\nBBB\n",
	})); err != nil {
		t.Fatalf("write src: %v", err)
	}

	var ready, done sync.WaitGroup
	ready.Add(1)
	done.Add(2)
	var moveErr, patchErr error
	go func() {
		defer done.Done()
		ready.Wait()
		_, moveErr = testTB.Execute(context.Background(), "file_move", fileJSON(map[string]any{
			"source": src, "destination": dst,
		}))
	}()
	go func() {
		defer done.Done()
		ready.Wait()
		_, patchErr = testTB.Execute(context.Background(), "file_patch", fileJSON(map[string]any{
			"path": src, "old_string": "AAA", "new_string": "aaa",
		}))
	}()

	doneCh := make(chan struct{})
	go func() { done.Wait(); close(doneCh) }()
	ready.Done() // release both goroutines simultaneously
	select {
	case <-doneCh:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: concurrent file_move + file_patch did not complete")
	}

	// move always succeeds: it removes src regardless of lock order.
	if moveErr != nil {
		t.Fatalf("file_move failed: %v", moveErr)
	}
	// patch may fail if the move already removed src before the patch read —
	// that is a valid serialized outcome, not a corruption. Check the error
	// semantically (fs.ErrNotExist) so the same assertion holds on every OS.
	if patchErr != nil && !errors.Is(patchErr, fs.ErrNotExist) {
		t.Fatalf("unexpected patch error: %v", patchErr)
	}

	// Structural invariants: src is gone, dst exists.
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("src must be removed after move, got err=%v", err)
	}
	dstData, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("dst must exist after move: %v", err)
	}
	// dst content is one of the valid serialized outcomes above.
	switch string(dstData) {
	case "AAA\nBBB\n", "aaa\nBBB\n":
	default:
		t.Fatalf("corrupted dst content: %q", dstData)
	}
}

// TestFileDeleteAndPatchConcurrentNoResurrection runs file_delete and
// file_patch concurrently on the same path. The lock serializes them, so
// whichever wins, the file is never resurrected:
//   - delete wins: the file is removed, then the patch reads a missing file
//     and fails without writing.
//   - patch wins: the patch writes new content, then the delete removes the
//     result.
//
// The deterministic invariant is that the file does not exist at the end. A
// lost delete (the old race: patch reads, delete removes, patch's atomic
// write recreates the file) would leave it present.
func TestFileDeleteAndPatchConcurrentNoResurrection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "victim.txt")
	if _, err := testTB.Execute(context.Background(), "file_write", fileJSON(map[string]any{
		"path": path, "content": "AAA\nBBB\n",
	})); err != nil {
		t.Fatalf("write: %v", err)
	}

	var ready, done sync.WaitGroup
	ready.Add(1)
	done.Add(2)
	var delErr, patchErr error
	go func() {
		defer done.Done()
		ready.Wait()
		_, delErr = testTB.Execute(context.Background(), "file_delete", fileJSON(map[string]any{
			"path": path,
		}))
	}()
	go func() {
		defer done.Done()
		ready.Wait()
		_, patchErr = testTB.Execute(context.Background(), "file_patch", fileJSON(map[string]any{
			"path": path, "old_string": "AAA", "new_string": "aaa",
		}))
	}()

	doneCh := make(chan struct{})
	go func() { done.Wait(); close(doneCh) }()
	ready.Done() // release both goroutines simultaneously
	select {
	case <-doneCh:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: concurrent file_delete + file_patch did not complete")
	}

	if delErr != nil {
		t.Fatalf("file_delete failed: %v", delErr)
	}
	// patch may fail if delete already removed the file — valid serialized
	// outcome, not a corruption. Check the error semantically (fs.ErrNotExist)
	// so the same assertion holds on every OS.
	if patchErr != nil && !errors.Is(patchErr, fs.ErrNotExist) {
		t.Fatalf("unexpected patch error: %v", patchErr)
	}

	// Deterministic invariant: the file is gone regardless of lock order.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file must not be resurrected after concurrent delete + patch: err=%v", err)
	}
}

// TestLockFilePathsDedupesAndOrders verifies the multi-path lock helper
// dedupes keys (filepath.Clean) and acquires them in sorted order, so two
// callers passing the same paths in different argument orders cannot
// deadlock. It also confirms the lock actually serializes: a second caller
// blocks until the first releases.
func TestLockFilePathsDedupesAndOrders(t *testing.T) {
	dir := t.TempDir()
	// Unique per-test keys avoid interference with the global filePathLocks
	// map; no files need to exist — the lock keys are just filepath.Clean strings.
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")

	acquired := make(chan struct{})
	release := make(chan struct{})
	contended := make(chan struct{})

	// Holder: lockFilePaths(b,a,b) dedupes to sorted [a,b].
	go func() {
		unlock := lockFilePaths(b, a, b)
		close(acquired)
		<-release
		unlock()
	}()

	<-acquired // holder has the lock

	// Contender: lockFilePaths(a,b) sorts to the same [a,b]. It must
	// block until the holder releases.
	go func() {
		unlock := lockFilePaths(a, b)
		close(contended)
		unlock()
	}()

	select {
	case <-contended:
		t.Fatal("contender acquired while holder still held the lock (no serialization)")
	case <-time.After(20 * time.Millisecond):
		// contender correctly blocked — release the holder.
	}

	close(release)

	select {
	case <-contended:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: contender never acquired after holder released")
	}
}
