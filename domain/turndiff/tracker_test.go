package turndiff

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gitBlobSHA1Hex(data string) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func trackerWithRoot(root string) *Tracker {
	return New(WithDisplayRoot(root))
}

func abs(t *testing.T, dir, name string) string {
	t.Helper()
	return filepath.Join(dir, name)
}

func mustDiff(t *testing.T, tr *Tracker) string {
	t.Helper()
	got, ok := tr.UnifiedDiff()
	if !ok {
		t.Fatal("expected unified diff")
	}
	return got
}

func TestAccumulatesAddThenUpdateAsSingleAdd(t *testing.T) {
	dir := t.TempDir()
	tr := trackerWithRoot(dir)
	path := abs(t, dir, "a.txt")

	tr.TrackDelta(AddFile(path, "foo\n", nil))
	tr.TrackDelta(UpdateFile(path, "foo\n", "foo\nbar\n", nil, nil))

	rightOID := gitBlobSHA1Hex("foo\nbar\n")
	want := fmt.Sprintf(`diff --git a/a.txt b/a.txt
new file mode %s
index %s..%s
--- %s
+++ b/a.txt
@@ -0,0 +1,2 @@
+foo
+bar
`, RegularFileMode, ZeroOID, rightOID, DevNull)
	if got := mustDiff(t, tr); got != want {
		t.Fatalf("diff mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestInvalidatedTrackerSuppressesExistingDiff(t *testing.T) {
	dir := t.TempDir()
	tr := trackerWithRoot(dir)
	path := abs(t, dir, "a.txt")
	tr.TrackDelta(AddFile(path, "foo\n", nil))
	tr.Invalidate()
	if _, ok := tr.UnifiedDiff(); ok {
		t.Fatal("invalidated tracker must have no unified diff")
	}
}

func TestInexactDeltaInvalidates(t *testing.T) {
	dir := t.TempDir()
	tr := trackerWithRoot(dir)
	path := abs(t, dir, "a.txt")
	tr.TrackDelta(AddFile(path, "foo\n", nil))
	tr.TrackDelta(Inexact())
	if _, ok := tr.UnifiedDiff(); ok {
		t.Fatal("inexact delta must invalidate the turn diff")
	}
}

func TestAccumulatesDelete(t *testing.T) {
	dir := t.TempDir()
	tr := trackerWithRoot(dir)
	path := abs(t, dir, "b.txt")
	tr.TrackDelta(DeleteFile(path, "x\n"))

	leftOID := gitBlobSHA1Hex("x\n")
	want := fmt.Sprintf(`diff --git a/b.txt b/b.txt
deleted file mode %s
index %s..%s
--- a/b.txt
+++ %s
@@ -1 +0,0 @@
-x
`, RegularFileMode, leftOID, ZeroOID, DevNull)
	if got := mustDiff(t, tr); got != want {
		t.Fatalf("diff mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestAccumulatesMoveAndUpdate(t *testing.T) {
	dir := t.TempDir()
	tr := trackerWithRoot(dir)
	src := abs(t, dir, "src.txt")
	dst := abs(t, dir, "dst.txt")
	tr.TrackDelta(UpdateFile(src, "line\n", "line2\n", StringPtr(dst), nil))

	leftOID := gitBlobSHA1Hex("line\n")
	rightOID := gitBlobSHA1Hex("line2\n")
	want := fmt.Sprintf(`diff --git a/src.txt b/dst.txt
index %s..%s
--- a/src.txt
+++ b/dst.txt
@@ -1 +1 @@
-line
+line2
`, leftOID, rightOID)
	if got := mustDiff(t, tr); got != want {
		t.Fatalf("diff mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestPureRenameYieldsNoDiff(t *testing.T) {
	dir := t.TempDir()
	tr := trackerWithRoot(dir)
	oldPath := abs(t, dir, "old.txt")
	newPath := abs(t, dir, "new.txt")
	tr.TrackDelta(UpdateFile(oldPath, "same\n", "same\n", StringPtr(newPath), nil))
	if _, ok := tr.UnifiedDiff(); ok {
		t.Fatal("pure rename with identical content must yield no diff")
	}
}

func TestAddOverExistingFileBecomesUpdate(t *testing.T) {
	dir := t.TempDir()
	tr := trackerWithRoot(dir)
	path := abs(t, dir, "dup.txt")
	tr.TrackDelta(AddFile(path, "after\n", StringPtr("before\n")))

	leftOID := gitBlobSHA1Hex("before\n")
	rightOID := gitBlobSHA1Hex("after\n")
	want := fmt.Sprintf(`diff --git a/dup.txt b/dup.txt
index %s..%s
--- a/dup.txt
+++ b/dup.txt
@@ -1 +1 @@
-before
+after
`, leftOID, rightOID)
	if got := mustDiff(t, tr); got != want {
		t.Fatalf("diff mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestDeleteThenReaddSamePathBecomesUpdate(t *testing.T) {
	dir := t.TempDir()
	tr := trackerWithRoot(dir)
	path := abs(t, dir, "cycle.txt")
	tr.TrackDelta(DeleteFile(path, "before\n"))
	tr.TrackDelta(AddFile(path, "after\n", nil))

	leftOID := gitBlobSHA1Hex("before\n")
	rightOID := gitBlobSHA1Hex("after\n")
	want := fmt.Sprintf(`diff --git a/cycle.txt b/cycle.txt
index %s..%s
--- a/cycle.txt
+++ b/cycle.txt
@@ -1 +1 @@
-before
+after
`, leftOID, rightOID)
	if got := mustDiff(t, tr); got != want {
		t.Fatalf("diff mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestMoveOverExistingDestinationWithoutContentChangeDeletesSourceOnly(t *testing.T) {
	dir := t.TempDir()
	tr := trackerWithRoot(dir)
	a := abs(t, dir, "a.txt")
	b := abs(t, dir, "b.txt")
	tr.TrackDelta(UpdateFile(a, "same\n", "same\n", StringPtr(b), StringPtr("same\n")))

	leftOID := gitBlobSHA1Hex("same\n")
	want := fmt.Sprintf(`diff --git a/a.txt b/a.txt
deleted file mode %s
index %s..%s
--- a/a.txt
+++ %s
@@ -1 +0,0 @@
-same
`, RegularFileMode, leftOID, ZeroOID, DevNull)
	if got := mustDiff(t, tr); got != want {
		t.Fatalf("diff mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestMoveOverExistingDestinationWithContentChangeDeletesSourceAndUpdatesDestination(t *testing.T) {
	dir := t.TempDir()
	tr := trackerWithRoot(dir)
	a := abs(t, dir, "a.txt")
	b := abs(t, dir, "b.txt")
	tr.TrackDelta(UpdateFile(a, "from\n", "new\n", StringPtr(b), StringPtr("existing\n")))

	leftA := gitBlobSHA1Hex("from\n")
	leftB := gitBlobSHA1Hex("existing\n")
	rightB := gitBlobSHA1Hex("new\n")
	want := fmt.Sprintf(`diff --git a/a.txt b/a.txt
deleted file mode %s
index %s..%s
--- a/a.txt
+++ %s
@@ -1 +0,0 @@
-from
diff --git a/b.txt b/b.txt
index %s..%s
--- a/b.txt
+++ b/b.txt
@@ -1 +1 @@
-existing
+new
`, RegularFileMode, leftA, ZeroOID, DevNull, leftB, rightB)
	if got := mustDiff(t, tr); got != want {
		t.Fatalf("diff mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestPreservesCommittedChangeOrderWithDeleteThenMoveOverwrite(t *testing.T) {
	dir := t.TempDir()
	tr := trackerWithRoot(dir)
	a := abs(t, dir, "a.txt")
	b := abs(t, dir, "b.txt")
	tr.TrackDelta(ExactChanges(
		DeleteChange(b, "existing\n"),
		UpdateChange(a, "from\n", "new\n", StringPtr(b), nil),
	))

	leftA := gitBlobSHA1Hex("from\n")
	leftB := gitBlobSHA1Hex("existing\n")
	rightB := gitBlobSHA1Hex("new\n")
	want := fmt.Sprintf(`diff --git a/a.txt b/a.txt
deleted file mode %s
index %s..%s
--- a/a.txt
+++ %s
@@ -1 +0,0 @@
-from
diff --git a/b.txt b/b.txt
index %s..%s
--- a/b.txt
+++ b/b.txt
@@ -1 +1 @@
-existing
+new
`, RegularFileMode, leftA, ZeroOID, DevNull, leftB, rightB)
	if got := mustDiff(t, tr); got != want {
		t.Fatalf("diff mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestReusesRenderedDiffsForUnchangedPaths(t *testing.T) {
	dir := t.TempDir()
	tr := trackerWithRoot(dir)
	tr.TrackDelta(AddFile(abs(t, dir, "a.txt"), "one\n", nil))
	if tr.renderedDiffsComputed() != 1 {
		t.Fatalf("rendered = %d, want 1", tr.renderedDiffsComputed())
	}
	tr.TrackDelta(AddFile(abs(t, dir, "b.txt"), "two\n", nil))
	if tr.renderedDiffsComputed() != 2 {
		t.Fatalf("rendered = %d, want 2", tr.renderedDiffsComputed())
	}
	_, _ = tr.UnifiedDiff()
	_, _ = tr.UnifiedDiff()
	if tr.renderedDiffsComputed() != 2 {
		t.Fatalf("reading cached aggregate re-rendered: %d", tr.renderedDiffsComputed())
	}
}

func TestRepeatedUpdatesOnlyRerenderTheTouchedPath(t *testing.T) {
	dir := t.TempDir()
	tr := trackerWithRoot(dir)
	tr.TrackDelta(AddFile(abs(t, dir, "stable.txt"), "stable\n", nil))
	tr.TrackDelta(AddFile(abs(t, dir, "hot.txt"), "value 0\n", nil))
	hot := abs(t, dir, "hot.txt")
	for value := 1; value <= 40; value++ {
		old := fmt.Sprintf("value %d\n", value-1)
		next := fmt.Sprintf("value %d\n", value)
		tr.TrackDelta(UpdateFile(hot, old, next, nil, nil))
	}
	if tr.renderedDiffsComputed() != 42 {
		t.Fatalf("rendered = %d, want 42", tr.renderedDiffsComputed())
	}
}

func TestLargeRewriteReturnsPromptlyAndPreservesExactContent(t *testing.T) {
	dir := t.TempDir()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "--quiet")
	run("config", "core.autocrlf", "false")

	var oldB, newB strings.Builder
	for i := 0; i < 48_000; i++ {
		fmt.Fprintf(&oldB, "old line %05d\n", i)
		fmt.Fprintf(&newB, "new line %05d\n", i)
	}
	oldContent := oldB.String()
	newContent := newB.String()
	path := filepath.Join(dir, "large.txt")
	if err := os.WriteFile(path, []byte(oldContent), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "large.txt")

	tr := trackerWithRoot(dir)
	tracked := trackedPath{path: path}
	started := time.Now()
	left, right := oldContent, newContent
	diff := tr.renderDiff(tracked, &left, tracked, &right)
	if diff == nil {
		t.Fatal("complete rewrite should produce a diff")
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("large rewrite took %s", elapsed)
	}

	cmd := exec.Command("git", "apply", "--whitespace=nowarn", "-")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(*diff)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git apply: %v\n%s\n%s", err, out, (*diff)[:min(len(*diff), 500)])
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != newContent {
		t.Fatal("applied diff did not restore exact new content")
	}
}
