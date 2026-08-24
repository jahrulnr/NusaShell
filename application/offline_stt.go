package application

import "context"

// This file implements Phase 1 of .experimental/NusaShell-STT-Technical-Design.md:
// the engine-neutral local/offline transcription port. The application and
// domain layers must never import whisper.cpp, sherpa-onnx, onnxruntime, or
// any CGO package — engines live behind OfflineTranscriber implementations in
// infrastructure/stt/<engine> (Phase 2), wired from cmd/nusashell.
//
// Design notes straight from the doc:
//   - Smallest useful interface first; streaming gets its own capability
//     interface only when a UI use case demands it (doc §4).
//   - Audio conversion/decoding is an infrastructure concern; the port deals
//     in raw encoded audio bytes plus explicit metadata (doc §17).
//   - Engines are long-lived: the implementation owns model lifetime and must
//     be safe for the concurrency it advertises (doc §16).

// OfflineTranscriber converts recorded audio to text using a LOCAL engine
// (no network). It is the offline sibling of SpeechTranscriber (the cloud
// /audio/transcriptions route) — read_media can prefer whichever is
// configured without changing its own logic.
type OfflineTranscriber interface {
	TranscribeOffline(ctx context.Context, req OfflineSTTRequest) (string, error)
}

// OfflineSTTRequest is one local transcription call.
type OfflineSTTRequest struct {
	Data       []byte // encoded audio bytes (wav/mp3/flac...); decoding is the engine adapter's job
	Language   string // optional ISO-639-1 hint ("id", "en"); empty = engine default/auto
	Model      string // optional: resolved ggml model file ("" = engine picks the first installed model in its model dir)
	Translate  bool   // optional: transcribe-and-translate to English when the engine supports it
	MaxSeconds int    // optional duration cap; 0 = engine default; protects against runaway jobs
}

// OfflineTranscriberStatus reports whether a local STT engine is usable
// right now. Implementations answer WITHOUT loading heavy models where
// possible (model files present? native init succeeded at startup?).
type OfflineTranscriberStatus interface {
	OfflineSTTAvailable() bool
	OfflineSTTUnavailableReason() string
}

// OfflineTranscriberFactory builds the configured local engine once at
// startup. Returns an error when no engine is bundled/configured; callers
// treat that as "offline STT disabled", not as a fatal error (doc §15).
type OfflineTranscriberFactory func() (OfflineTranscriber, error)
