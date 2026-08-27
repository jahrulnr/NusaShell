package whisper

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"nusashell/application"
)

// fakeWhisper is a whisper-cli stand-in: it records the full argv string to
// the file named by WFAKE_ARGS (tests assert the exact CLI shape against it)
// and writes "fake transcript" to whatever follows -of, mimicking
// whisper-cli's `<output>.txt` convention. Exit code is controlled by the
// test via WFAKE_EXIT.
const fakeWhisperBody = `#!/bin/sh
printf '%s\n' "$*" > "${WFAKE_ARGS:-/dev/null}"
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "-of" ]; then out="$2"; shift; fi
  shift
done
[ -n "$out" ] && printf 'fake transcript' > "$out.txt"
exit ${WFAKE_EXIT:-0}
`

func writeFakeBinary(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "whisper-cli")
	if err := os.WriteFile(path, []byte(fakeWhisperBody), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// quietEnv freezes the globals the resolution order reads so each test only
// sees what it sets itself. PATH is repointed to a temp dir so a system-wide
// whisper-cli install does not leak into the "no engine" assertions; /bin and
// /usr/bin are preserved so the fake binary's /bin/sh shebang still works.
func quietEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"WHISPER_BIN", "WFAKE_ARGS", "WFAKE_EXIT"} {
		t.Setenv(k, "")
	}
	emptyPath := t.TempDir()
	t.Setenv("PATH", emptyPath+":/bin:/usr/bin")
}

func TestLookupBinaryOverrideManagedAndFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake whisper-cli is a POSIX shell script; managed lookup also expects .exe on Windows")
	}
	quietEnv(t)
	dir := t.TempDir()

	// runtime layout: <dir>/whisper/<goos>-<goarch>/whisper-cli is the
	// one-click installer target; the model dir lives at <dir>/models/stt.
	modelsDir := filepath.Join(dir, "models", "stt")
	eng := New("", modelsDir)

	if _, err := eng.lookupBinary(); err == nil {
		t.Fatal("no engine anywhere should be an error")
	}
	if got := eng.OfflineSTTUnavailableReason(); !strings.Contains(got, "engine not found") {
		t.Errorf("reason should name the engine, got %q", got)
	}

	managed := writeFakeBinary(t, filepath.Join(dir, "whisper", runtime.GOOS+"-"+runtime.GOARCH))
	if got, err := eng.lookupBinary(); err != nil || got != managed {
		t.Errorf("managed lookup: got %q (%v), want %q", got, err, managed)
	}

	other := writeFakeBinary(t, filepath.Join(dir, "custom"))
	eng2 := New(other, modelsDir)
	if got, err := eng2.lookupBinary(); err != nil || got != other {
		t.Errorf("override priority broken: got %q (%v), want %q", got, err, other)
	}
}

func TestResolveModelFallbackAndByName(t *testing.T) {
	quietEnv(t)
	dir := t.TempDir()
	if _, err := resolveModel(dir, ""); err == nil {
		t.Fatal("empty dir should fail")
	}
	model := filepath.Join(dir, "ggml-small.bin")
	if err := os.WriteFile(model, []byte("model"), 0o644); err != nil {
		t.Fatal(err)
	}
	if path, err := resolveModel(dir, ""); err != nil || path != model {
		t.Errorf("first-installed fallback: path=%q err=%v", path, err)
	}
	if path, err := resolveModel(dir, "ggml-small.bin"); err != nil || path != model {
		t.Errorf("by name: path=%q err=%v", path, err)
	}
	if _, err := resolveModel(dir, "ggml-tiny.bin"); err == nil {
		t.Error("missing model by name must fail")
	}
}

func TestTranscribeEndToEndEmitsExpectedFlags(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake whisper-cli is a POSIX shell script")
	}
	quietEnv(t)
	dir := t.TempDir()
	t.Setenv("WFAKE_ARGS", filepath.Join(dir, "args.log"))
	bin := writeFakeBinary(t, filepath.Join(dir, "bin"))
	modelDir := filepath.Join(dir, "models", "stt")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	model := filepath.Join(modelDir, "ggml-small.bin")
	if err := os.WriteFile(model, []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}

	eng := New(bin, modelDir)
	eng.SetPrompt("NusaShell")
	text, err := eng.TranscribeOffline(context.Background(), application.OfflineSTTRequest{
		Data:     tinyWav(),
		Language: "id",
		Model:    "ggml-small.bin",
	})
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if text != "fake transcript" {
		t.Errorf("transcript = %q", text)
	}

	argStr, err := os.ReadFile(filepath.Join(dir, "args.log"))
	if err != nil {
		t.Fatalf("arg log: %v", err)
	}
	full := string(argStr)
	// Exact flag set the adapter is locked to (whisper.cpp 1.x CLI shape).
	for _, needle := range []string{"-m " + model, "-f ", "-nt", "-of ", "-l id", "--prompt NusaShell"} {
		if !strings.Contains(full, needle) {
			t.Errorf("expected %q in argv: %q", needle, strings.TrimSpace(full))
		}
	}
	if strings.Contains(full, "-tr") {
		t.Error("-tr must not appear for a plain transcription")
	}
}

func TestTranscribeHonorsTranslateAndMissingModel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake whisper-cli is a POSIX shell script")
	}
	quietEnv(t)
	dir := t.TempDir()
	t.Setenv("WFAKE_ARGS", filepath.Join(dir, "args.log"))
	bin := writeFakeBinary(t, filepath.Join(dir, "bin"))
	modelDir := filepath.Join(dir, "models", "stt")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	model := filepath.Join(modelDir, "ggml-base.bin")
	if err := os.WriteFile(model, []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := New(bin, modelDir)
	eng.SetPrompt("")

	out, err := eng.TranscribeOffline(context.Background(), application.OfflineSTTRequest{
		Data:      tinyWav(),
		Model:     "ggml-base.bin",
		Translate: true,
	})
	if err != nil {
		t.Fatalf("translate pass: %v", err)
	}
	if out == "" {
		t.Error("translate transcript empty")
	}
	argStr, _ := os.ReadFile(filepath.Join(dir, "args.log"))
	if !strings.Contains(string(argStr), "-tr") {
		t.Errorf("translate flag missing in %q", strings.TrimSpace(string(argStr)))
	}

	// Missing model: per-call actionable error, engine stays wired.
	if _, err := eng.TranscribeOffline(context.Background(), application.OfflineSTTRequest{
		Data: tinyWav(), Model: "ggml-famous.bin",
	}); err == nil {
		t.Error("missing model must error")
	}
}

func TestTranscribePropagatesCLIFailure(t *testing.T) {
	quietEnv(t)
	dir := t.TempDir()
	t.Setenv("WFAKE_EXIT", "1")
	bin := writeFakeBinary(t, filepath.Join(dir, "bin"))
	modelDir := filepath.Join(dir, "models", "stt")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "ggml-base.bin"), []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := New(bin, modelDir)
	if _, err := eng.TranscribeOffline(context.Background(), application.OfflineSTTRequest{Data: tinyWav()}); err == nil {
		t.Fatal("a failing CLI invocation must produce an error")
	}
}

func TestTranscribeRejectsEmptyAudio(t *testing.T) {
	eng := New("", t.TempDir())
	if _, err := eng.TranscribeOffline(context.Background(), application.OfflineSTTRequest{}); err == nil {
		t.Error("empty audio should fail")
	}
}
