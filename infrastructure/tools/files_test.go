package tools

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
