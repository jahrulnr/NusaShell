package sttinstall

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"nusashell/contracts"
)

// Progress rides the stt.install.* bus events. BytesFetched/BytesTotal are
// running counters WITHIN the current phase; the FE computes download speed
// from the deltas and shows an indeterminate bar when BytesTotal == 0.
type Progress = contracts.STTInstallProgressDTO


const (
	whisperReleasesBase = "https://github.com/ggml-org/whisper.cpp/releases/download"
	modelsBaseDefault   = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main"
	downloadTimeout     = 40 * time.Minute // per-file; large models on slow links stay valid
)

// Installer downloads and stages the offline STT engine + GGML models.
type Installer struct {
	dataDir     string
	releaseBase string // github release download base (tag-aware)
	modelsBase  string // huggingface resolve base
	client      *http.Client
}

// New builds an installer rooted at the NusaShell data directory.
// releaseBase/modelsBase may be empty (production endpoints); tests point
// them at httptest servers.
func New(dataDir, releaseBase, modelsBase string) *Installer {
	in := &Installer{
		dataDir:     dataDir,
		releaseBase: strings.TrimRight(releaseBase, "/"),
		modelsBase:  strings.TrimRight(modelsBase, "/"),
		client:      &http.Client{Timeout: downloadTimeout},
	}
	if in.releaseBase == "" {
		in.releaseBase = whisperReleasesBase + "/" + releaseTag
	}
	if in.modelsBase == "" {
		in.modelsBase = modelsBaseDefault
	}
	return in
}

// Layout (doc §9 style, piper-TTS mirror):
//
//	<data>/whisper/<goos>-<goarch>/whisper-cli[.exe]   engine binary
//	<data>/models/stt/ggml-<id>.bin                    GGML models
func (in *Installer) engineDir() string {
	return filepath.Join(in.dataDir, "whisper", runtime.GOOS+"-"+runtime.GOARCH)
}

func engineExecutableName() string {
	if runtime.GOOS == "windows" {
		return "whisper-cli.exe"
	}
	return "whisper-cli"
}

func (in *Installer) engineBinary() string {
	p := filepath.Join(in.engineDir(), engineExecutableName())
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return p
	}
	return ""
}

func (in *Installer) modelsDir() string { return filepath.Join(in.dataDir, "models", "stt") }

func (in *Installer) modelPath(id string) string { return filepath.Join(in.modelsDir(), id+".bin") }

// lookupEngine finds a usable whisper-cli anywhere: managed copy,
// WHISPER_BIN, or PATH.
func (in *Installer) lookupEngine() (path, source string) {
	if p := in.engineBinary(); p != "" {
		return p, "managed"
	}
	if e := strings.TrimSpace(os.Getenv("WHISPER_BIN")); e != "" {
		if p, err := exec.LookPath(e); err == nil {
			return p, "env"
		}
	}
	if p, err := exec.LookPath("whisper-cli"); err == nil {
		return p, "path"
	}
	return "", ""
}

// Status snapshots the install surface for the Settings card.
func (in *Installer) Status() contracts.STTInstallStatusResult {
	_, supported := engineAsset(runtime.GOOS, runtime.GOARCH)
	enginePath, src := in.lookupEngine()
	models := make([]contracts.STTModelDTO, 0, len(Models))
	anyModel := false
	for _, m := range Models {
		installed := fileOk(in.modelPath(m.ID))
		if installed {
			anyModel = true
		}
		models = append(models, contracts.STTModelDTO{
			ID: m.ID, Label: m.Label, SizeBytes: m.Size, Installed: installed, Default: m.Default,
		})
	}
	res := contracts.STTInstallStatusResult{
		Supported:       supported,
		EngineInstalled: enginePath != "",
		EnginePath:      enginePath,
		EngineSource:    src,
		DiskFreeBytes:   diskFree(in.dataDir),
		Ready:           enginePath != "" && anyModel,
		Models:          models,
	}
	res.Reason = in.nextAction(enginePath)
	return res
}

// nextAction is the one-line "what to do" the UI renders: install the
// engine, then a model, or package-ready.
func (in *Installer) nextAction(enginePath string) string {
	if _, ok := engineAsset(runtime.GOOS, runtime.GOARCH); !ok {
		return "unsupported-platform"
	}
	if enginePath == "" {
		return "engine"
	}
	if matches, _ := filepath.Glob(filepath.Join(in.modelsDir(), "ggml-*.bin")); len(matches) == 0 {
		return "model"
	}
	return ""
}

// diskFree reports free bytes on the volume backing dir (0 = unknown).
func diskFree(dir string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0
	}
	return int64(st.Bavail) * st.Bsize
}

func fileOk(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Install downloads the engine (only when the platform auto-installs it
// AND none is found) then the chosen model, verifying sha256 along the way.
// The App validates the model id, guards the in-flight single-flight, and
// routes Progress to the Bus.
func (in *Installer) Install(ctx context.Context, modelID string, report func(Progress)) error {
	model, ok := modelByID(modelID)
	if !ok {
		return fmt.Errorf("stt: unknown model %q", modelID)
	}
	if _, ok := engineAsset(runtime.GOOS, runtime.GOARCH); !ok && in.engineBinary() == "" {
		if _, src := in.lookupEngine(); src == "" {
			return errors.New("stt: platform unsupported — install whisper-cli manually (Settings → guidance)")
		}
	}

	if enginePath, _ := in.lookupEngine(); enginePath == "" {
		if err := in.installEngine(ctx, report); err != nil {
			return err
		}
	}
	if err := in.installModel(ctx, model, report); err != nil {
		return err
	}

	if report != nil {
		report(Progress{Phase: PhaseVerify, BytesFetched: 1, BytesTotal: 1, Message: "Verifying installation"})
	}
	if enginePath, _ := in.lookupEngine(); enginePath == "" {
		return errors.New("stt: verify — engine missing after install")
	}
	if st, err := os.Stat(in.modelPath(model.ID)); err != nil {
		return errors.New("stt: verify — model missing after install")
	} else if st.Size() != model.Size {
		return fmt.Errorf("stt: verify — model size mismatch: got %d want %d", st.Size(), model.Size)
	}
	return nil
}

// installEngine downloads the official release archive and stages whisper-cli.
func (in *Installer) installEngine(ctx context.Context, report func(Progress)) error {
	asset, ok := engineAsset(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return errors.New("stt: engine install — platform unsupported")
	}
	archive := filepath.Join(os.TempDir(), fmt.Sprintf("nusashell-stt-engine-%d.%s", os.Getpid(), asset.Kind))
	if err := downloadToFile(ctx, in.client, fmt.Sprintf("%s/%s", in.releaseBase, asset.Name), archive, func(p Progress) {
		p.Phase = PhaseBinary
		p.BytesTotal = maxOf(p.BytesTotal, 0)
		if p.Message == "" {
			p.Message = "Downloading whisper.cpp engine"
		}
		if report != nil {
			report(p)
		}
	}); err != nil {
		return fmt.Errorf("stt: engine download: %w", err)
	}
	defer os.Remove(archive)

	if err := os.MkdirAll(in.engineDir(), 0o755); err != nil {
		return err
	}
	if err := extractEngine(archive, asset.Kind, in.engineDir(), engineExecutableName()); err != nil {
		return fmt.Errorf("stt: engine unpack: %w", err)
	}
	enginePath := filepath.Join(in.engineDir(), engineExecutableName())
	if err := os.Chmod(enginePath, 0o755); err != nil {
		return err
	}
	return nil
}

// extractEngine copies the engineFile entry out of an archive layout that may
// nest it one directory level deep (bXX/whisper-cli.exe).
func extractEngine(archivePath, kind, dstDir, engineFile string) error {
	extract := func(save func(name string, r io.Reader) error) error {
		return saveEngineFromArchive(archivePath, kind, save)
	}
	return extract(func(name string, r io.Reader) error {
		if filepath.Base(name) != engineFile {
			return nil
		}
		f, err := os.Create(filepath.Join(dstDir, engineFile))
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(f, r); err != nil {
			return err
		}
		return nil
	})
}

// saveEngineFromArchive walks the archive and calls save once for the engine.
func saveEngineFromArchive(archivePath, kind string, save func(name string, r io.Reader) error) error {
	if kind == "zip" {
		zr, err := zip.OpenReader(archivePath)
		if err != nil {
			return err
		}
		defer zr.Close()
		for _, zf := range zr.File {
			if zf.FileInfo().IsDir() {
				continue
			}
			rc, err := zf.Open()
			if err != nil {
				return err
			}
			err = save(zf.Name, rc)
			_ = rc.Close()
			if err != nil {
				return err
			}
		}
		return nil
	}
	gz, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr, err := gzip.NewReader(gz)
	if err != nil {
		return err
	}
	defer tr.Close()
	reader := tar.NewReader(tr)
	for {
		hdr, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if err := save(hdr.Name, reader); err != nil {
			return err
		}
	}
	return nil
}

// installModel downloads one GGML model into a .download temp and then
// verifies size + sha256 before renaming into place. On any failure the
// temp is removed so a retry starts clean.
func (in *Installer) installModel(ctx context.Context, m Model, report func(Progress)) error {
	url := fmt.Sprintf("%s/%s", in.modelsBase, m.HFPath)
	if err := os.MkdirAll(in.modelsDir(), 0o755); err != nil {
		return err
	}
	tmp := in.modelPath(m.ID) + ".download"
	if err := downloadToFile(ctx, in.client, url, tmp, func(p Progress) {
		p.Phase = PhaseModel
		if p.BytesTotal <= 0 {
			p.BytesTotal = m.Size // known catalog size — keeps the bar determinate
		}
		if p.Message == "" {
			p.Message = "Downloading model " + m.ID
		}
		if report != nil {
			report(p)
		}
	}); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("stt: model download: %w", err)
	}
	if err := verifySHA256(tmp, m.SHA256); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, in.modelPath(m.ID)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// verifySHA256 matches the downloaded bytes against the LFS oid shipped in
// the catalog. Empty "want" (tests) passes.
func verifySHA256(path, want string) error {
	if want == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("stt: sha256 mismatch: got %s want %s", got, want)
	}
	return nil
}

// downloadToFile streams url into dst, reporting per-chunk progress. The
// caller owns cleanup and final size verification.
func downloadToFile(ctx context.Context, client *http.Client, url, dst string, report func(Progress)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("stt: download %s: HTTP %d", url, resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	total := resp.ContentLength
	buf := make([]byte, 128*1024)
	var fetched int64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			fetched += int64(n)
			if report != nil {
				report(Progress{BytesFetched: fetched, BytesTotal: total})
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	return nil
}

func maxOf(a, b int64) int64 {
	if a < b {
		return b
	}
	return a
}
