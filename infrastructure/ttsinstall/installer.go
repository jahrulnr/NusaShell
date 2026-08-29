// Package ttsinstall implements the one-click offline TTS installer: it
// downloads the piper release binary for the running platform and the
// selected voice model from rhasspy/piper-voices into the NusaShell data
// directory, reporting progress per phase so the Settings dialog can show
// a live bar.
//
// Layout after install (doc §9 style):
//
//	<data>/piper/<goos>-<goarch>/piper[.exe]   binary + espeak-ng-data + libs
//	<data>/models/tts/<voice>.onnx(.json)      voice models (PIPER_VOICES_DIR layout)
//
// The engine in infrastructure/tts/piper picks both locations up with zero
// configuration: the binary is found on disk and the voices directory is
// the default one.
package ttsinstall

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"nusashell/infrastructure/nusatemp"
)

// Progress phases reported by Install.
const (
	PhaseBinary = "binary" // release archive download + extraction
	PhaseVoice  = "voice"  // .onnx (+.onnx.json) download
	PhaseVerify = "verify" // post-install availability check
)

// Voice describes one installable offline TTS voice.
type Voice struct {
	ID       string // stable id, also the onnx file base name
	Label    string // human-readable picker label
	Language string // BCP-47-ish language tag ("id_ID")
	Size     int64  // exact onnx size in bytes (verified against piper-voices)
	HFPath   string // path inside rhasspy/piper-voices on Hugging Face
}

// Voices is the curated install catalog. Sizes are the exact LFS byte
// counts of the onnx models at the time they were verified via the HF API;
// they drive the download progress bar only — a mismatch never fails the
// install.
var Voices = []Voice{
	{
		ID:       "id_ID-news_tts-medium",
		Label:    "Bahasa Indonesia — news_tts (medium)",
		Language: "id_ID",
		Size:     62_950_044,
		HFPath:   "id/id_ID/news_tts/medium/id_ID-news_tts-medium.onnx",
	},
	{
		ID:       "en_US-lessac-high",
		Label:    "English (US) — lessac (high)",
		Language: "en_US",
		Size:     113_895_201,
		HFPath:   "en/en_US/lessac/high/en_US-lessac-high.onnx",
	},
}

const (
	releaseBase = "https://github.com/rhasspy/piper/releases/download/2023.11.14-2"
	voicesBase  = "https://huggingface.co/rhasspy/piper-voices/resolve/main"

	downloadTimeout = 30 * time.Minute // per-file; large voices on slow links stay valid
)

// Progress reports installer progress to the application layer.
type Progress struct {
	Phase        string
	BytesFetched int64
	BytesTotal   int64 // 0 = unknown
	Message      string
}

// VoiceStatus is one catalog voice's on-disk state for Status().
type VoiceStatus struct {
	ID        string
	Label     string
	Language  string
	SizeBytes int64
	Installed bool
}

// StatusResult snapshots the install state right now.
type StatusResult struct {
	BinaryInstalled bool
	Voices          []VoiceStatus
	VoicesOnDisk    int64 // any *.onnx present, incl. non-catalog voices
	// Ready mirrors the generate_speech gate: a usable piper engine AND at
	// least one voice present.
	Ready bool
}

// Installer downloads and stages the offline TTS engine. base override is
// for tests (empty = production endpoints).
type Installer struct {
	dataDir   string
	base      string
	client    *http.Client
	voicesDir string // resolved lazily via voicesDirectory()
}

// New builds an Installer rooted at the NusaShell data directory. baseURL
// overrides both release and voice endpoints when non-empty (tests).
func New(dataDir, baseURL string) *Installer {
	return &Installer{
		dataDir: dataDir,
		base:    strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: downloadTimeout},
	}
}

func (in *Installer) platformDir() string {
	return filepath.Join(in.dataDir, "piper", runtime.GOOS+"-"+runtime.GOARCH)
}

// binaryPath returns where the piper executable lands after extraction.
func (in *Installer) binaryPath() string {
	name := "piper"
	if runtime.GOOS == "windows" {
		name = "piper.exe"
	}
	return filepath.Join(in.platformDir(), name)
}

// voicesDirectory mirrors the piper engine's resolution order
// (PIPER_VOICES_DIR wins over the default <data>/models/tts).
func (in *Installer) voicesDirectory() string {
	if dir := os.Getenv("PIPER_VOICES_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(in.dataDir, "models", "tts")
}

// status snapshots what is installed on disk right now.
func (in *Installer) status() StatusResult {
	out := StatusResult{Voices: make([]VoiceStatus, 0, len(Voices))}
	dir := in.voicesDirectory()
	for _, v := range Voices {
		st := VoiceStatus{ID: v.ID, Label: v.Label, Language: v.Language, SizeBytes: v.Size}
		if _, err := os.Stat(filepath.Join(dir, v.ID+".onnx")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, v.ID+".onnx.json")); err == nil {
				st.Installed = true
			}
		}
		out.Voices = append(out.Voices, st)
	}
	out.BinaryInstalled = in.binaryOnDisk()
	// Ready counts ANY *.onnx in the voices dir: the piper engine resolves
	// its default voice by globbing there, so a user-placed non-catalog
	// voice still makes speech live. This mirrors the engine's own lookup,
	// not our catalog.
	matches, _ := filepath.Glob(filepath.Join(dir, "*.onnx"))
	out.VoicesOnDisk = int64(len(matches))
	out.Ready = in.engineUsable() && len(matches) > 0
	return out
}

// binaryOnDisk reports whether the managed binary exists at its expected
// location. A user-provided PIPER_BIN/PATH install also satisfies the
// engine but is not reported here (it needs no management).
func (in *Installer) binaryOnDisk() bool {
	_, err := os.Stat(in.binaryPath())
	return err == nil
}

// engineUsable mirrors runtime engine resolution: any binary that the
// piper engine can actually execute (PIPER_BIN, PATH, or the managed
// copy). It feeds status.Ready so the UI knows speech can run NOW even
// when the managed install hasn't happened (a manual binary works too).
func (in *Installer) engineUsable() bool {
	if bin := os.Getenv("PIPER_BIN"); bin != "" {
		if _, err := os.Stat(bin); err == nil {
			return true
		}
	}
	if _, err := exec.LookPath("piper"); err == nil {
		return true
	}
	return in.binaryOnDisk()
}

// Install fetches the piper binary (when neither PIPER_BIN nor PATH has it)
// plus the requested voice, reporting progress per phase. Safe to call
// again: already-satisfied parts are skipped.
func (in *Installer) install(ctx context.Context, voiceID string, report func(Progress)) error {
	if report == nil {
		report = func(Progress) {}
	}
	var voice *Voice
	for i := range Voices {
		if Voices[i].ID == voiceID {
			voice = &Voices[i]
			break
		}
	}
	if voice == nil {
		ids := make([]string, 0, len(Voices))
		for _, v := range Voices {
			ids = append(ids, v.ID)
		}
		return fmt.Errorf("ttsinstall: unknown voice %q (catalog: %s)", voiceID, strings.Join(ids, ", "))
	}
	if err := os.MkdirAll(in.dataDir, 0o700); err != nil {
		return fmt.Errorf("ttsinstall: data dir: %w", err)
	}
	in.voicesDir = in.voicesDirectory()
	if err := os.MkdirAll(in.voicesDir, 0o755); err != nil {
		return fmt.Errorf("ttsinstall: voices dir: %w", err)
	}

	if !in.managedBinaryReady() {
		var asset string
		switch {
		case runtime.GOOS == "linux" && runtime.GOARCH == "amd64":
			asset = "piper_linux_x86_64.tar.gz"
		case runtime.GOOS == "linux" && runtime.GOARCH == "arm64":
			asset = "piper_linux_aarch64.tar.gz"
		case runtime.GOOS == "linux" && runtime.GOARCH == "arm":
			asset = "piper_linux_armv7l.tar.gz"
		case runtime.GOOS == "darwin" && runtime.GOARCH == "amd64":
			asset = "piper_macos_x64.tar.gz"
		case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
			asset = "piper_macos_aarch64.tar.gz"
		case runtime.GOOS == "windows" && runtime.GOARCH == "amd64":
			asset = "piper_windows_amd64.zip"
		}
		if asset == "" {
			return fmt.Errorf("ttsinstall: no official piper build for %s/%s; set PIPER_BIN to an existing binary", runtime.GOOS, runtime.GOARCH)
		}
		url := fmt.Sprintf("%s/%s", in.binaryAssetBase(), asset)
		report(Progress{Phase: PhaseBinary, Message: "Downloading piper engine"})
		var xerr error
		if strings.HasSuffix(asset, ".zip") {
			xerr = in.fetchArchive(ctx, url, func(f *os.File) error { return extractZipTree(f, in.platformDir(), "piper/") })
		} else {
			xerr = in.fetchArchive(ctx, url, func(f *os.File) error { return extractTarGzTree(f, in.platformDir(), "piper/") })
		}
		if xerr != nil {
			return fmt.Errorf("ttsinstall: %w", xerr)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(in.binaryPath(), 0o755); err != nil {
				return fmt.Errorf("ttsinstall: chmod binary: %w", err)
			}
		}
	}

	report(Progress{Phase: PhaseVoice, BytesTotal: voice.Size, Message: "Downloading " + voice.Label})
	onnxPath := filepath.Join(in.voicesDir, voice.ID+".onnx")
	jsonPath := filepath.Join(in.voicesDir, voice.ID+".onnx.json")
	needVoice := false
	for _, suffix := range []string{".onnx", ".onnx.json"} {
		if _, err := os.Stat(filepath.Join(in.voicesDir, voice.ID+suffix)); err != nil {
			needVoice = true
			break
		}
	}
	if needVoice {
		if err := in.fetchToFile(ctx, fmt.Sprintf("%s/%s", in.voicesBase(), voice.HFPath), onnxPath, report); err != nil {
			_ = os.Remove(onnxPath) // never keep partial models
			return fmt.Errorf("ttsinstall: %w", err)
		}
		if err := in.fetchToFile(ctx, fmt.Sprintf("%s/%s.json", in.voicesBase(), voice.HFPath), jsonPath, report); err != nil {
			_ = os.Remove(onnxPath)
			_ = os.Remove(jsonPath)
			return fmt.Errorf("ttsinstall: %w", err)
		}
	}

	report(Progress{Phase: PhaseVerify, Message: "Verifying installation"})
	if _, err := os.Stat(onnxPath); err != nil {
		return fmt.Errorf("ttsinstall: verify failed: %w", err)
	}
	if _, err := os.Stat(jsonPath); err != nil {
		return fmt.Errorf("ttsinstall: verify failed: %w", err)
	}
	if !in.managedBinaryReady() {
		return fmt.Errorf("ttsinstall: verify failed: piper binary incomplete at %s", in.platformDir())
	}
	report(Progress{Phase: PhaseVerify, Message: "Offline TTS ready"})
	return nil
}

// managedBinaryReady is install-gate-only: true when the MANAGED copy is
// complete (binary + espeak-ng-data present). A PATH/PIPER_BIN binary must
// not skip the managed download — the engine resolver prefers the managed
// location, so a half-extracted tree there would shadow the working one.
func (in *Installer) managedBinaryReady() bool {
	if _, err := os.Stat(in.binaryPath()); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(in.platformDir(), "espeak-ng-data")); err != nil {
		return false
	}
	return true
}

func (in *Installer) releaseBase() string {
	if in.base != "" {
		return in.base + "/bin"
	}
	return releaseBase
}

// binaryAssetBase returns the URL prefix for a piper release asset: with a
// test base it is <base>/bin/<tag>/ (the test server serves per-tag), in
// production it is the release download root .../download/<tag>/.
func (in *Installer) binaryAssetBase() string {
	if in.base != "" {
		return in.releaseBase() + "/2023.11.14-2"
	}
	return releaseBase
}

func (in *Installer) voicesBase() string {
	if in.base != "" {
		return in.base + "/voice"
	}
	return voicesBase
}

func (in *Installer) fetchArchive(ctx context.Context, url string, use func(*os.File) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := in.client.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d from %s", resp.StatusCode, url)
	}
	tmp, err := nusatemp.MkdirTemp("tts-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	f, err := os.CreateTemp(tmp, "archive-*")
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return fmt.Errorf("download interrupted: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		f.Close()
		return err
	}
	if err := use(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// fetchToFile streams url to path with progress callbacks. Writes go to a
// temp sibling first so an interrupted download never leaves a corrupt
// model behind.
func (in *Installer) fetchToFile(ctx context.Context, url, path string, report func(Progress)) error {
	if _, err := os.Stat(path); err == nil {
		return nil // already there (resumable by re-run semantics)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := in.client.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d from %s", resp.StatusCode, url)
	}
	total := resp.ContentLength
	tmp, err := os.CreateTemp(filepath.Dir(path), ".partial-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	var fetched int64
	buf := make([]byte, 256*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := tmp.Write(buf[:n]); werr != nil {
				tmp.Close()
				return werr
			}
			fetched += int64(n)
			report(Progress{Phase: PhaseVoice, BytesFetched: fetched, BytesTotal: total})
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			tmp.Close()
			return fmt.Errorf("download interrupted: %w", rerr)
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if total > 0 && fetched != total {
		return fmt.Errorf("download truncated: got %d of %d bytes", fetched, total)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	report(Progress{Phase: PhaseVoice, BytesFetched: total, BytesTotal: total})
	return nil
}

// extractTarGzTree unpacks entries rooted under prefix into dest,
// preserving relative layout (piper/, piper/espeak-ng-data/...).
func extractTarGzTree(f *os.File, dest, prefix string) error {
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("bad archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("bad archive: %w", err)
		}
		if err := writeArchiveEntry(dest, hdr.Name, hdr.Typeflag == tar.TypeDir, func(w io.Writer) error {
			_, err := io.Copy(w, tr)
			return err
		}, prefix); err != nil {
			return err
		}
		// Symlinks/hardlinks carry no regular-file payload; recreate them.
		if hdr.Typeflag == tar.TypeSymlink || hdr.Typeflag == tar.TypeLink {
			if err := linkArchiveEntry(dest, hdr.Name, hdr.Linkname, hdr.Typeflag == tar.TypeLink, prefix); err != nil {
				return err
			}
		}
	}
	return nil
}

// extractZipTree is the zip twin of extractTarGzTree (zip needs a
// seekable reader, hence the *os.File).
func extractZipTree(f *os.File, dest, prefix string) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		return fmt.Errorf("bad archive: %w", err)
	}
	for _, zf := range zr.File {
		zfName := filepath.ToSlash(zf.Name)
		isDir := zf.FileInfo().IsDir()
		if err := func() error {
			rc, err := zf.Open()
			if err != nil {
				return fmt.Errorf("bad archive: %w", err)
			}
			defer rc.Close()
			return writeArchiveEntry(dest, zfName, isDir, func(w io.Writer) error {
				_, err := io.Copy(w, rc)
				return err
			}, prefix)
		}(); err != nil {
			return err
		}
	}
	return nil
}

// writeArchiveEntry maps one archive member under dest/prefix with a
// zip-slip guard and writes it via the provided copier. Symlink members
// (no payload) are skipped here and handled by linkArchiveEntry.
func writeArchiveEntry(dest, name string, isDir bool, copy func(io.Writer) error, prefix string) error {
	target, ok := safeArchiveTarget(dest, name, prefix)
	if !ok {
		return nil // skip stray entries outside the piper root
	}
	if isDir {
		return os.MkdirAll(target, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if err := copy(dst); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

// linkArchiveEntry recreates a tar symlink/hardlink under dest/prefix.
func linkArchiveEntry(dest, name, linkname string, hard bool, prefix string) error {
	target, ok := safeArchiveTarget(dest, name, prefix)
	if !ok {
		return nil
	}
	_ = os.Remove(target) // a skipped placeholder may already exist
	if hard {
		source, ok := safeArchiveTarget(dest, filepath.Join(prefix, filepath.Clean(filepath.FromSlash(linkname))), prefix)
		if !ok {
			return fmt.Errorf("unsafe archive link target %q", linkname)
		}
		return os.Link(source, target)
	}
	return os.Symlink(filepath.FromSlash(linkname), target)
}

// safeArchiveTarget resolves one archive member to its destination path,
// enforcing the prefix and guarding against path traversal. Returns ok=false
// for entries outside the expected root.
func safeArchiveTarget(dest, name, prefix string) (string, bool) {
	clean := filepath.Clean(filepath.FromSlash(name))
	// The prefix is slash-form ("piper/"); compare it in native form so
	// Windows paths (piper\piper.exe) match — otherwise every entry is
	// silently skipped and extraction produces nothing.
	nativePrefix := filepath.FromSlash(prefix)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", false // zip-slip guard
	}
	if !strings.HasPrefix(clean, nativePrefix) || clean == "." {
		return "", false
	}
	rel := strings.TrimPrefix(clean, nativePrefix)
	target := filepath.Join(dest, rel)
	if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) {
		return "", false // zip-slip guard
	}
	return target, true
}
