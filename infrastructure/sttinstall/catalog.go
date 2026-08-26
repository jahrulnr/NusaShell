package sttinstall

// Catalog of installable whisper.cpp assets. Sizes and SHA-256 digests
// come straight from the Hugging Face LFS manifest + the GitHub release
// manifest of ggml-org/whisper.cpp and are the exact artifacts we install
// (.experimental/offline-stt-assessment.md §2.4). Update = change one file.

const (
	// releaseTag pins the verified b4938 release of ggml-org/whisper.cpp
	// (asset names + sha256 digests below match its manifest, audited
	// 2026-08-23). A catalog bump touches only this file.
	releaseTag = "b4938"
)

// Progress phases reported by Install.
const (
	PhaseBinary = "binary"
	PhaseModel  = "model"
	PhaseVerify = "verify"
)

// Model is one installable whisper GGML model on Hugging Face.
type Model struct {
	ID      string // stable id, equals the picker value and the on-disk .bin base name
	Label   string // human-readable picker label
	Size    int64  // exact LFS byte count (verified 2026-08-23)
	SHA256  string // LFS oid = sha256 of the file
	HFPath  string // path inside ggerganov/whisper.cpp on Hugging Face
	Default bool   // recommended default install (Settings: Install)
}

// Models is the curated, language-gated catalog: every entry must serve
// Indonesian AND English (multilingual Whisper weights). The `.en` variants
// are deliberately excluded (.experimental/offline-stt-assessment.md §2.4).
var Models = []Model{
	{
		ID:     "ggml-tiny",
		Label:  "Whisper tiny — fastest, rough accuracy",
		Size:   77_691_713,
		SHA256: "be07e048e1e599ad46341c8d2a135645097a538221678b7acdd1b1919c6e1b21",
		HFPath: "ggml-tiny.bin",
	},
	{
		ID:     "ggml-base",
		Label:  "Whisper base — light",
		Size:   147_951_465,
		SHA256: "60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe",
		HFPath: "ggml-base.bin",
	},
	{
		ID:     "ggml-small-q5_1",
		Label:  "Whisper small (q5_1) — compact, close to full small",
		Size:   190_085_487,
		SHA256: "ae85e4a935d7a567bd102fe55afc16bb595bdb618e11b2fc7591bc08120411bb",
		HFPath: "ggml-small-q5_1.bin",
	},
	{
		ID:      "ggml-small",
		Label:   "Whisper small — recommended default (id + en)",
		Size:    487_601_967,
		SHA256:  "1be3a9b2063867b937e64e2ec7483364a79917e157fa98c5d94b5c1fffea987b",
		HFPath:  "ggml-small.bin",
		Default: true,
	},
	{
		ID:     "ggml-large-v3-turbo-q5_0",
		Label:  "Whisper large-v3-turbo (q5_0) — very accurate, compact",
		Size:   574_041_195,
		SHA256: "394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2",
		HFPath: "ggml-large-v3-turbo-q5_0.bin",
	},
	{
		ID:     "ggml-large-v3-turbo",
		Label:  "Whisper large-v3-turbo — most accurate",
		Size:   1_624_555_275,
		SHA256: "1fc70f774d38eb169993ac391eea357ef47c88757ef72ee5943879b7e8e2bc69",
		HFPath: "ggml-large-v3-turbo.bin",
	},
}

// EngineAsset describes one official whisper.cpp CLI release for a platform.
type EngineAsset struct {
	Name   string // asset file name inside the release
	Kind   string // "tar.gz" | "zip"
	DiskMB int64  // rough extracted size hint for the UI bar
}

// engineAssets mirrors the official release manifest (ggml macOS ships no CLI,
// surface via SupportedErrors + guide modal).
var engineAssets = map[string]EngineAsset{
	"linux/amd64":   {Name: "whisper-bin-ubuntu-x64.tar.gz", Kind: "tar.gz", DiskMB: 10},
	"linux/arm64":   {Name: "whisper-bin-ubuntu-arm64.tar.gz", Kind: "tar.gz", DiskMB: 5},
	"windows/amd64": {Name: "whisper-bin-x64.zip", Kind: "zip", DiskMB: 9},
	"windows/386":   {Name: "whisper-bin-Win32.zip", Kind: "zip", DiskMB: 6},
}

// engineAsset resolves the release archive for the running platform.
func engineAsset(goos, goarch string) (EngineAsset, bool) {
	a, ok := engineAssets[goos+"/"+goarch]
	return a, ok
}
