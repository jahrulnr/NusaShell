package application

import (
	"context"
	"fmt"

	"nusashell/contracts"
)

// Offline TTS one-click install: the installer port lives in
// infrastructure (network + filesystem heavy); the App owns the single
// in-flight install, RPC handlers, and progress events on the Bus.

// TTSInstaller is the port for the offline TTS engine installer.
type TTSInstaller interface {
	Status() contracts.TTSInstallStatusResult
	Install(ctx context.Context, voiceID string, report func(contracts.TTSInstallProgressDTO)) error
}

// handleTTSSettingsInstallStatus snapshots what is installed on disk plus
// whether an install is currently running.
func (a *App) handleTTSSettingsInstallStatus() (any, *contracts.RPCError) {
	res := contracts.TTSInstallStatusResult{}
	if a.TTSInstaller != nil {
		res = a.TTSInstaller.Status()
	}
	res.Running = a.ttsInstallRunning()
	return res, nil
}

// handleTTSSettingsInstallStart kicks off one offline TTS install in the
// background. Progress flows to the UI via tts.install.* Bus events; the
// RPC returns immediately so the dialog can render live progress.
func (a *App) handleTTSSettingsInstallStart(req contracts.TTSInstallStartRequest) (any, *contracts.RPCError) {
	if a.TTSInstaller == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "offline TTS installer is not available in this build"}
	}
	if req.VoiceID == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "voice_id is required"}
	}
	if !knownTTSVoice(req.VoiceID) {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: fmt.Sprintf("unknown voice %q", req.VoiceID)}
	}
	if !a.ttsInstallBegin() {
		return contracts.TTSInstallStartResult{Started: false, Running: true, Message: "an offline TTS install is already running"}, nil
	}

	ctx := context.Background()
	go func() {
		defer a.ttsInstallEnd()
		err := a.TTSInstaller.Install(ctx, req.VoiceID, func(p contracts.TTSInstallProgressDTO) {
			p.VoiceID = req.VoiceID // stamp once here; adapters fill phase/bytes only
			a.Bus.Emit(contracts.EventTTSInstallProgress, p)
		})
		if err != nil {
			a.log("error", "tts", "offline TTS install failed: %v", err)
			a.Bus.Emit(contracts.EventTTSInstallError, contracts.TTSInstallProgressDTO{
				VoiceID: req.VoiceID, Message: err.Error(),
			})
			return
		}
		a.log("info", "tts", "offline TTS installed: %s", req.VoiceID)
		a.Bus.Emit(contracts.EventTTSInstallDone, contracts.TTSInstallProgressDTO{
			VoiceID: req.VoiceID, Phase: "verify", Message: "Offline TTS ready",
		})
	}()
	return contracts.TTSInstallStartResult{Started: true, Running: true}, nil
}

// ttsInstallRun guards the single in-flight install.
func (a *App) ttsInstallRunning() bool {
	a.ttsInstallMu.Lock()
	defer a.ttsInstallMu.Unlock()
	return a.ttsInstallActive
}

func (a *App) ttsInstallBegin() bool {
	a.ttsInstallMu.Lock()
	defer a.ttsInstallMu.Unlock()
	if a.ttsInstallActive {
		return false
	}
	a.ttsInstallActive = true
	return true
}

func (a *App) ttsInstallEnd() {
	a.ttsInstallMu.Lock()
	defer a.ttsInstallMu.Unlock()
	a.ttsInstallActive = false
}

// knownTTSVoice validates against the wire-visible catalog without
// dragging infrastructure types into the application layer.
func knownTTSVoice(id string) bool {
	for _, v := range contracts.OfflineTTSVoiceIDs {
		if v == id {
			return true
		}
	}
	return false
}
