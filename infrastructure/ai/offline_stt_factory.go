package ai

import (
	"os"
	"strings"

	"nusashell/application"
	"nusashell/infrastructure/stt/whisper"
)

// NewOfflineTranscriberFactory wires the offline STT runtime behind the
// read_media degradation ladder (.experimental/offline-stt-assessment.md).
// Model resolution happens per call, entirely observable on disk:
//
//  1. settings.stt_offline_model (bare file name) — set by the Settings UI
//     after a one-click install
//  2. NUSASHELL_STT_MODEL env var (path or bare name, doc §9 override)
//  3. the first ggml-*.bin under <data>/models/stt/
//
// The factory NEVER fails at wiring time: missing engine or model becomes a
// per-call error, so read_media degrades to the cloud rung or guidance
// instead of the app refusing to start — the no-CGO external-binary runtime
// is soft by contract (.experimental/offline-stt-assessment.md §3.b).
func NewOfflineTranscriberFactory(settings application.SettingsStore, dataDir string) application.OfflineTranscriberFactory {
	modelProbe := func() string {
		if settings != nil {
			if name := strings.TrimSpace(settings.Get().STTOfflineModel); name != "" {
				return name
			}
		}
		return os.Getenv("NUSASHELL_STT_MODEL")
	}
	langProbe := func() string {
		if settings != nil {
			return settings.Get().STTOfflineLanguage
		}
		return ""
	}
	return func() (application.OfflineTranscriber, error) {
		return whisper.NewTranscriber(dataDir, modelProbe, langProbe), nil
	}
}
