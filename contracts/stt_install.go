package contracts

// Offline STT one-click install: wire contract for the Settings flow. The
// installer lives in infrastructure/sttinstall (piper-TTS mirror); the App
// owns the single in-flight install and the Bus events
// (.experimental/offline-stt-assessment.md §4).

// STTInstallProgressDTO rides the stt.install.* events.
type STTInstallProgressDTO struct {
	ModelID      string `json:"model_id"`
	Phase        string `json:"phase"`                   // binary | model | verify
	BytesFetched int64  `json:"bytes_fetched,omitempty"` // running counter within the phase
	BytesTotal   int64  `json:"bytes_total,omitempty"`   // 0 = unknown (UI hides the bar)
	Message      string `json:"message,omitempty"`       // short human-readable line
}

// STTModelDTO describes one installable whisper GGML model.
type STTModelDTO struct {
	ID        string `json:"id"`         // stable id, equals the picker value
	Label     string `json:"label"`      // human-readable ("Whisper small — multilingual (default)")
	SizeBytes int64  `json:"size_bytes"` // exact GGML file size (verified against the HF catalog)
	Installed bool   `json:"installed"`  // ggml-<id>.bin present in <data>/models/stt
	Default   bool   `json:"default"`    // the recommendstep default install
}

// STTInstallStatusResult snapshots the install surface for the Settings card.
type STTInstallStatusResult struct {
	Supported bool `json:"supported"` // this OS/arch has an official whisper-cli release
	// EngineInstalled is true when a usable whisper-cli binary exists
	// (managed copy under <data>/whisper/<platform>/, WHISPER_BIN, or PATH).
	EngineInstalled bool   `json:"engine_installed"`
	EnginePath      string `json:"engine_path,omitempty"`
	EngineSource    string `json:"engine_source,omitempty"` // managed | env | path
	DiskFreeBytes   int64  `json:"disk_free_bytes,omitempty"`
	ActiveModel     string `json:"active_model,omitempty"` // settings.stt_offline_model in effect
	Running         bool   `json:"running"`
	// Ready mirrors the runtime gate of read_audio: engine resolvable AND at
	// least one GGML model installed — no settings flag involved.
	Ready   bool   `json:"ready"`
	Reason  string `json:"reason,omitempty"` // next step for the user
	Models  []STTModelDTO `json:"models"`
}

// STTInstallStartRequest kicks off one model install (engine included when
// the platform auto-installs it).
type STTInstallStartRequest struct {
	ModelID string `json:"model_id"`
}

// STTInstallStartResult reports whether the install was launched.
type STTInstallStartResult struct {
	Started bool   `json:"started"`
	Running bool   `json:"running"`
	Message string `json:"message,omitempty"`
}
