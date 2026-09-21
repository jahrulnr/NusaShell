package install

import (
	"archive/tar"
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/infrastructure/nusatemp"
	"nusashell/pkg/archive"
	"nusashell/pkg/fetch"
	"nusashell/pkg/httpclient"
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
		client:      httpclient.NewWithTimeout(downloadTimeout),
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
		_, statErr := os.Stat(in.modelPath(m.ID))
		installed := statErr == nil
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

// Install downloads the engine (only when the platform auto-installs it
// AND none is found) then the chosen model, verifying sha256 along the way.
// The App validates the model id, guards the in-flight single-flight, and
// routes Progress to the Bus.
func (in *Installer) Install(ctx context.Context, modelID string, report func(Progress)) error {
	var model Model
	found := false
	for _, m := range Models {
		if m.ID == modelID {
			model = m
			found = true
			break
		}
	}
	if !found {
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
	dir, err := nusatemp.Dir()
	if err != nil {
		return err
	}
	archive := filepath.Join(dir, fmt.Sprintf("stt-engine-%d.%s", os.Getpid(), asset.Kind))
	if err := downloadToFile(ctx, in.client, fmt.Sprintf("%s/%s", in.releaseBase, asset.Name), archive, func(p Progress) {
		p.Phase = PhaseBinary
		if p.BytesTotal < 0 {
			p.BytesTotal = 0
		}
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
		return archive.WalkZip(&zr.Reader, func(zf *zip.File) (bool, error) {
			if zf.FileInfo().IsDir() {
				return true, nil
			}
			rc, err := zf.Open()
			if err != nil {
				return false, err
			}
			err = save(zf.Name, rc)
			_ = rc.Close()
			if err != nil {
				return false, err
			}
			return true, nil
		})
	}
	gz, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer gz.Close()
	return archive.WalkTarGz(gz, func(hdr *tar.Header, body io.Reader) (bool, error) {
		if hdr.Typeflag != tar.TypeReg {
			return true, nil
		}
		return true, save(hdr.Name, body)
	})
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
	err := fetch.VerifyFile(path, want)
	var mm *fetch.MismatchError
	if errors.As(err, &mm) {
		return fmt.Errorf("stt: sha256 mismatch: got %s want %s", mm.Got, mm.Want)
	}
	return err
}

// downloadToFile streams url into dst, reporting per-chunk progress. The
// caller owns cleanup and final size verification.
func downloadToFile(ctx context.Context, client *http.Client, url, dst string, report func(Progress)) error {
	return fetch.File(ctx, client, url, dst, &fetch.Options{
		AcceptStatus: func(code int) bool { return code/100 == 2 },
		Report: func(p fetch.Progress) {
			if report != nil {
				report(Progress{BytesFetched: p.BytesFetched, BytesTotal: p.BytesTotal})
			}
		},
	})
}
