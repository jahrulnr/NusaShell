package ttsinstall

import (
	"context"

	"nusashell/contracts"
)

// Compile-time check: the installer satisfies the application port.
var _ interface {
	Status() contracts.TTSInstallStatusResult
	Install(ctx context.Context, voiceID string, report func(contracts.TTSInstallProgressDTO)) error
} = (*Installer)(nil)

// Status converts the internal snapshot into the wire DTO.
func (in *Installer) Status() contracts.TTSInstallStatusResult {
	internal := in.status()
	out := contracts.TTSInstallStatusResult{BinaryInstalled: internal.BinaryInstalled, Ready: internal.Ready}
	out.Voices = make([]contracts.TTSVoiceDTO, 0, len(internal.Voices))
	for _, v := range internal.Voices {
		out.Voices = append(out.Voices, contracts.TTSVoiceDTO{
			ID: v.ID, Label: v.Label, Language: v.Language, SizeBytes: v.SizeBytes, Installed: v.Installed,
		})
	}
	return out
}

// Install runs the download/extraction pipeline, translating progress
// callbacks into wire DTOs.
func (in *Installer) Install(ctx context.Context, voiceID string, report func(contracts.TTSInstallProgressDTO)) error {
	return in.install(ctx, voiceID, func(p Progress) {
		if report == nil {
			return
		}
		report(contracts.TTSInstallProgressDTO{
			Phase: p.Phase, BytesFetched: p.BytesFetched, BytesTotal: p.BytesTotal, Message: p.Message,
		})
	})
}
