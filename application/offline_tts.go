package application

// Offline speech synthesis port (local piper engine). Mirrors
// OfflineTranscriber: the application never imports piper/CGO — the adapter
// lives in infrastructure and is wired from cmd/nusashell.

type OfflineSynthesizer interface {
	Available() bool
	UnavailableReason() string
	Synthesize(req TTSRequest) (*TTSResult, error)
}
