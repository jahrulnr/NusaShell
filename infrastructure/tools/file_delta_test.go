package tools

import (
	"testing"

	"nusashell/domain/turndiff"
)

func TestFileWriteRecordsAddDelta(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/hello.txt"
	ctx, cap := turndiff.WithCapture(t.Context())
	if _, _, err := executeFileToolCtx(ctx, "file_write", []byte(`{"path":"`+jsonPath(path)+`","content":"hello\n"}`)); err != nil {
		t.Fatalf("file_write: %v", err)
	}
	d, ok := cap.Take()
	if !ok || !d.Exact || len(d.Changes) != 1 {
		t.Fatalf("delta = %+v ok=%v", d, ok)
	}
	ch := d.Changes[0]
	if ch.Kind != turndiff.ChangeAdd || ch.Content != "hello\n" || ch.OverwrittenContent != nil {
		t.Fatalf("change = %+v", ch)
	}
}

func TestFileWriteOverExistingRecordsOverwrittenContent(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/dup.txt"
	if _, _, err := executeFileTool("file_write", []byte(`{"path":"`+jsonPath(path)+`","content":"before\n"}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cap := turndiff.WithCapture(t.Context())
	if _, _, err := executeFileToolCtx(ctx, "file_write", []byte(`{"path":"`+jsonPath(path)+`","content":"after\n"}`)); err != nil {
		t.Fatal(err)
	}
	d, ok := cap.Take()
	if !ok || !d.Exact {
		t.Fatalf("delta = %+v ok=%v", d, ok)
	}
	if d.Changes[0].OverwrittenContent == nil || *d.Changes[0].OverwrittenContent != "before\n" {
		t.Fatalf("overwritten = %v", d.Changes[0].OverwrittenContent)
	}
}

func TestFilePatchRecordsUpdateDelta(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/log.txt"
	if _, _, err := executeFileTool("file_write", []byte(`{"path":"`+jsonPath(path)+`","content":"line1\n"}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cap := turndiff.WithCapture(t.Context())
	if _, _, err := executeFileToolCtx(ctx, "file_patch", []byte(`{"path":"`+jsonPath(path)+`","old_string":"line1\n","new_string":"line2\n"}`)); err != nil {
		t.Fatal(err)
	}
	d, ok := cap.Take()
	if !ok || !d.Exact || d.Changes[0].Kind != turndiff.ChangeUpdate {
		t.Fatalf("delta = %+v ok=%v", d, ok)
	}
	if d.Changes[0].OldContent != "line1\n" || d.Changes[0].NewContent != "line2\n" {
		t.Fatalf("update = %+v", d.Changes[0])
	}
}

func TestFilePatchPreviewDoesNotRecordDelta(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/log.txt"
	if _, _, err := executeFileTool("file_write", []byte(`{"path":"`+jsonPath(path)+`","content":"line1\n"}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cap := turndiff.WithCapture(t.Context())
	if _, _, err := executeFileToolCtx(ctx, "file_patch", []byte(`{"path":"`+jsonPath(path)+`","old_string":"line1\n","new_string":"x\n","preview":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, ok := cap.Take(); ok {
		t.Fatal("preview must not record a turn diff")
	}
}

func TestFileDeleteRecordsDeleteDelta(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/gone.txt"
	if _, _, err := executeFileTool("file_write", []byte(`{"path":"`+jsonPath(path)+`","content":"x\n"}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cap := turndiff.WithCapture(t.Context())
	if _, _, err := executeFileToolCtx(ctx, "file_delete", []byte(`{"path":"`+jsonPath(path)+`"}`)); err != nil {
		t.Fatal(err)
	}
	d, ok := cap.Take()
	if !ok || d.Changes[0].Kind != turndiff.ChangeDelete || d.Changes[0].Content != "x\n" {
		t.Fatalf("delta = %+v ok=%v", d, ok)
	}
}

func TestFileMkdirIsNotTracked(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/nested"
	ctx, cap := turndiff.WithCapture(t.Context())
	if _, _, err := executeFileToolCtx(ctx, "file_mkdir", []byte(`{"path":"`+jsonPath(path)+`"}`)); err != nil {
		t.Fatal(err)
	}
	if _, ok := cap.Take(); ok {
		t.Fatal("file_mkdir must not be tracked")
	}
}

func TestFileCopyRecordsAddAtDestination(t *testing.T) {
	dir := t.TempDir()
	src := dir + "/src.txt"
	dst := dir + "/dst.txt"
	if _, _, err := executeFileTool("file_write", []byte(`{"path":"`+jsonPath(src)+`","content":"copyme\n"}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cap := turndiff.WithCapture(t.Context())
	if _, _, err := executeFileToolCtx(ctx, "file_copy", []byte(`{"source":"`+jsonPath(src)+`","destination":"`+jsonPath(dst)+`"}`)); err != nil {
		t.Fatal(err)
	}
	d, ok := cap.Take()
	if !ok || d.Changes[0].Kind != turndiff.ChangeAdd || d.Changes[0].Content != "copyme\n" {
		t.Fatalf("delta = %+v ok=%v", d, ok)
	}
}

func TestFileMoveRecordsUpdateWithMovePath(t *testing.T) {
	dir := t.TempDir()
	src := dir + "/old.txt"
	dst := dir + "/new.txt"
	if _, _, err := executeFileTool("file_write", []byte(`{"path":"`+jsonPath(src)+`","content":"same\n"}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cap := turndiff.WithCapture(t.Context())
	if _, _, err := executeFileToolCtx(ctx, "file_move", []byte(`{"source":"`+jsonPath(src)+`","destination":"`+jsonPath(dst)+`"}`)); err != nil {
		t.Fatal(err)
	}
	d, ok := cap.Take()
	if !ok || d.Changes[0].Kind != turndiff.ChangeUpdate || d.Changes[0].MovePath == nil {
		t.Fatalf("delta = %+v ok=%v", d, ok)
	}
}

func TestDirectoryDeleteIsInexact(t *testing.T) {
	dir := t.TempDir()
	nested := dir + "/folder"
	if _, _, err := executeFileTool("file_mkdir", []byte(`{"path":"`+jsonPath(nested)+`"}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cap := turndiff.WithCapture(t.Context())
	if _, _, err := executeFileToolCtx(ctx, "file_delete", []byte(`{"path":"`+jsonPath(nested)+`","recursive":true}`)); err != nil {
		t.Fatal(err)
	}
	d, ok := cap.Take()
	if !ok || d.Exact {
		t.Fatalf("directory delete must be inexact, delta=%+v ok=%v", d, ok)
	}
}

func TestBinaryWriteIsInexact(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/bin.dat"
	ctx, cap := turndiff.WithCapture(t.Context())
	if _, _, err := executeFileToolCtx(ctx, "file_write", []byte(`{"path":"`+jsonPath(path)+`","encoding":"base64","content":"AAA="}`)); err != nil {
		t.Fatal(err)
	}
	d, ok := cap.Take()
	if !ok || d.Exact {
		t.Fatalf("binary write must be inexact, delta=%+v ok=%v", d, ok)
	}
}
