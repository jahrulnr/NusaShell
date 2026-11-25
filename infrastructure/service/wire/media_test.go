package wire

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
)

func localMediaRoot(dir string) MediaRoot {
	return MediaRoot{Dir: dir, Store: repoworkspace.NewLocal()}
}

func mustAttachment(t *testing.T, uri, mt string) valueobject.Attachment {
	t.Helper()
	a, err := valueobject.NewAttachment(uri, mt)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// Verification reads the file through the door that serves it. A symlink
// inside the workspace that points outside is refused by both, so the hash
// reported here can only be the hash of the file send-time would hand over.
func TestVerifyPinnedFileRefusesASymlinkServingRefuses(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.bin")
	if err := os.WriteFile(outside, []byte("outside bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.bin")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	escaped := mustAttachment(t, "file://"+link, "application/octet-stream")
	sum := sha256.Sum256([]byte("outside bytes"))
	escaped.SetSHA256(hex.EncodeToString(sum[:]))

	if _, _, err := ResolveBase64(escaped, localMediaRoot(root)); err == nil {
		t.Fatal("serving followed a symlink out of the workspace")
	}
	if err := VerifyPinnedFile(escaped, localMediaRoot(root)); err == nil {
		t.Fatal("verification followed a symlink serving refuses")
	}
}

// A pinned file outside the root crosses under its pin; a changed one keeps its
// own verdict instead of blurring into "unreadable".
func TestVerifyPinnedFileKeepsThePinVerdict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(path, []byte("original bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	att := mustAttachment(t, "file://"+path, "image/png")
	sum := sha256.Sum256([]byte("original bytes"))
	att.SetSHA256(hex.EncodeToString(sum[:]))
	root := localMediaRoot(t.TempDir())

	if err := VerifyPinnedFile(att, root); err != nil {
		t.Fatalf("an intact pinned file outside the root = %v", err)
	}
	if err := os.WriteFile(path, []byte("different bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPinnedFile(att, root); !errors.Is(err, ErrPinChanged) {
		t.Fatalf("a changed file = %v, want ErrPinChanged", err)
	}
	// The serving path agrees, and says so the same way.
	if _, _, err := ResolveBase64(att, root); !errors.Is(err, ErrPinChanged) {
		t.Fatalf("serving a changed file = %v, want ErrPinChanged", err)
	}
}

// An unpinned file has nothing to verify; the confinement rule belongs to the
// door that serves it, and that door still refuses a file outside the root.
func TestVerifyPinnedFileLeavesAnUnpinnedFileToTheServingRule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	att := mustAttachment(t, "file://"+path, "text/plain")
	root := localMediaRoot(t.TempDir())
	if err := VerifyPinnedFile(att, root); err != nil {
		t.Fatalf("an unpinned file claimed a pin: %v", err)
	}
	if _, err := readLocal(att, root); err == nil {
		t.Fatal("an unpinned file outside the root must not cross")
	}
}

func TestResolveURI(t *testing.T) {
	raw := []byte("abc")
	if u, err := ResolveURI(mustAttachment(t, "https://x/img.png", "image/png"), MediaRoot{}); err != nil || u != "https://x/img.png" {
		t.Fatal("https passthrough broken")
	}
	png := noisyPNG(t, 16, 16, false)
	dataURI := DataURI("image/png", png)
	if u, err := ResolveURI(mustAttachment(t, dataURI, "image/png"), MediaRoot{}); err != nil || u != dataURI {
		t.Fatal("data: passthrough broken")
	}

	root := t.TempDir()
	rootPath, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "x.bin")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := ResolveURI(mustAttachment(t, "file://"+path, "application/octet-stream"), localMediaRoot(rootPath))
	if err != nil {
		t.Fatal(err)
	}
	if u != "data:application/octet-stream;base64,"+base64.StdEncoding.EncodeToString(raw) {
		t.Fatalf("file:// resolve = %q", u)
	}
	if _, err := ResolveURI(mustAttachment(t, "file://"+filepath.Join(root, "missing.bin"), "application/octet-stream"), localMediaRoot(rootPath)); err == nil {
		t.Fatal("missing file should error")
	}
}

// A pinned attachment must resolve to the exact bytes the producer
// hashed — a swapped file fails closed rather than silently sending
// different content.
func TestResolveURIPin(t *testing.T) {
	raw := []byte("abc")
	sum := sha256.Sum256(raw)
	pin := hex.EncodeToString(sum[:])

	root := t.TempDir()
	rootPath, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "x.bin")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	a := mustAttachment(t, "file://"+path, "application/octet-stream")
	a.SetSHA256(pin)
	if _, err := ResolveURI(a, localMediaRoot(rootPath)); err != nil {
		t.Fatalf("matching pin should resolve: %v", err)
	}

	if err := os.WriteFile(path, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveURI(a, localMediaRoot(rootPath)); err == nil {
		t.Fatal("swapped content must fail the pin")
	}

	// A pin on a remote ref cannot be verified without fetching — fail
	// closed rather than silently trusting it.
	a = mustAttachment(t, "https://x/img.png", "image/png")
	a.SetSHA256(pin)
	if _, err := ResolveURI(a, MediaRoot{}); err == nil {
		t.Fatal("pinned remote ref must be rejected")
	}

	// data: URIs carry their bytes inline — pins verify those too.
	a = mustAttachment(t, "data:application/octet-stream;base64,"+base64.StdEncoding.EncodeToString(raw), "application/octet-stream")
	a.SetSHA256(pin)
	if _, err := ResolveURI(a, MediaRoot{}); err != nil {
		t.Fatalf("pinned data URI should resolve: %v", err)
	}
	other := sha256.Sum256([]byte("other"))
	a.SetSHA256(hex.EncodeToString(other[:]))
	if _, err := ResolveURI(a, MediaRoot{}); err == nil {
		t.Fatal("pinned data URI with wrong digest must fail")
	}
}

func TestResolveURIConfinesAndBoundsLocalFiles(t *testing.T) {
	root := t.TempDir()
	rootPath, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(rootPath, "inside.bin")
	outside := filepath.Join(t.TempDir(), "outside.bin")
	content := []byte("small binary payload")
	if err := os.WriteFile(inside, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, content, 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(rootPath, "escape.bin")
	if err := os.Symlink(outside, symlink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	att := mustAttachment(t, "file://"+inside, "application/octet-stream")
	if _, err := ResolveURI(att, localMediaRoot(rootPath)); err != nil {
		t.Fatalf("workspace file should resolve: %v", err)
	}
	att = mustAttachment(t, "file://"+outside, "application/octet-stream")
	if _, err := ResolveURI(att, localMediaRoot(rootPath)); err == nil {
		t.Fatal("file outside workspace must not resolve")
	}
	att = mustAttachment(t, "file://"+symlink, "application/octet-stream")
	if _, err := ResolveURI(att, localMediaRoot(rootPath)); err == nil {
		t.Fatal("symlink escape must not resolve")
	}

	large := filepath.Join(rootPath, "large.png")
	f, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(11 << 20); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	att = mustAttachment(t, "file://"+large, "image/png")
	if _, err := ResolveURI(att, localMediaRoot(rootPath)); err == nil {
		t.Fatal("file over the media cap must not resolve")
	}
}

func TestResolveBase64(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("sound"))
	data, mediaType, err := ResolveBase64(mustAttachment(t, "data:audio/mpeg;base64,"+b64, "audio/mpeg"), MediaRoot{})
	if err != nil || data != b64 || mediaType != "audio/mpeg" || FormatName(mediaType) != "mp3" {
		t.Fatalf("data uri: %q %q %v", data, mediaType, err)
	}
	if _, _, err := ResolveBase64(mustAttachment(t, "https://x/a.mp3", "audio/mpeg"), MediaRoot{}); err == nil {
		t.Fatal("https audio should be rejected — nothing to embed")
	}
}

func TestFormatName(t *testing.T) {
	for in, want := range map[string]string{
		"audio/mpeg": "mp3", "audio/x-wav": "wav", "audio/mp4": "mp4",
	} {
		if got := FormatName(in); got != want {
			t.Fatalf("%s → %s, want %s", in, got, want)
		}
	}
}

func TestResolveURIHostPathRequiresPin(t *testing.T) {
	root := t.TempDir()
	rootPath, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "shot.png")
	content := noisyPNG(t, 16, 16, false)
	if err := os.WriteFile(outside, content, 0o600); err != nil {
		t.Fatal(err)
	}
	fileRoot := localMediaRoot(rootPath)

	// Unmediated outside references stay rejected — only a pinned one
	// crosses, since the pin proves a tool hashed this exact file.
	att := mustAttachment(t, "file://"+outside, "image/png")
	if _, err := ResolveURI(att, fileRoot); err == nil {
		t.Fatal("unpinned outside attachment must not resolve")
	}
	sum := sha256.Sum256(content)
	att.SetSHA256(hex.EncodeToString(sum[:]))
	u, err := ResolveURI(att, fileRoot)
	if err != nil {
		t.Fatalf("pinned outside attachment should resolve: %v", err)
	}
	if !strings.HasPrefix(u, "data:image/png;base64,") {
		t.Fatalf("resolved = %q", u)
	}
	// A swapped pin degrades to rejection, not to unchecked bytes.
	att.SetSHA256("0" + hex.EncodeToString(sum[:])[1:])
	if _, err := ResolveURI(att, fileRoot); err == nil {
		t.Fatal("wrong pin must not resolve")
	}
}
