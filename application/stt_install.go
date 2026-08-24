package application

import (
	"context"
	"fmt"
	"time"

	"nusashell/contracts"
)

// Offline STT one-click install: the installer port lives in
// infrastructure/sttinstall (whisper-cli binary + GGML models); the App
// owns the single in-flight install, RPC handlers, and progress events on
// the Bus — the same shape as tts_install.go
// (.experimental/offline-stt-assessment.md §4).

// STTInstaller is the port for the offline STT engine installer.
type STTInstaller interface {
	Status() contracts.STTInstallStatusResult
	Install(ctx context.Context, modelID string, report func(contracts.STTInstallProgressDTO)) error
}

// handleSTTSettingsInstallStatus snapshots the disk state plus the live
// install hinge. The wheel's "reason" states what to do next
// (engine → model →pingus).
func (a *App) handleSTTSettingsInstallStatus() (any, *contracts.RPCError) {
	if a.STTInstaller == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "offline STT installer unavailable in this build"}
	}
	res := a.STTInstaller.Status()
	// Active model shown in the card — derives from disk + settings only.
	if a.Settings != nil {
		res.ActiveModel = a.Settings.Get().STTOfflineModel
	}
	res.Running = a.sttInstallRunning()
	return res, nil
}

// handleSTTSettingsInstallStart launches one install goroutine and
// immediately returns; progress rides the Bus as stt.install.* events.
func (a *App) handleSTTSettingsInstallStart(req contracts.STTInstallStartRequest) (any, *contracts.RPCError) {
	if a.STTInstaller == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "offline STT installer unavailable in this build"}
	}
	if req.ModelID == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "model_id is required"}
	}
	if !knownSTTModel(req.ModelID) {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: fmt.Sprintf("unknown STT model %q", req.ModelID)}
	}
	if !a.sttInstallBegin() {
		return contracts.STTInstallStartResult{Started: false, Running: true, Message: "an offline STT install is already running"}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.sttInstallMu.Lock()
	a.sttInstallCancel = cancel
	a.sttInstallDoneCh = make(chan struct{})
	a.sttInstallMu.Unlock()
	go func() {
		defer a.sttInstallEnd()
		err := a.STTInstaller.Install(ctx, req.ModelID, func(p contracts.STTInstallProgressDTO) {
			p.ModelID = req.ModelID
			a.Bus.Emit(contracts.EventSTTInstallProgress, p)
		})
		if err == nil {
			a.log("info", "stt", "offline STT installed: %s", req.ModelID)
			a.Bus.Emit(contracts.EventSTTInstallDone, contracts.STTInstallProgressDTO{
				ModelID: req.ModelID, Phase: "verify", Message: "Offline STT ready",
			})
			return
		}
		if ctx.Err() == context.Canceled {
			// Cancelled — the sync handler already surfaced the state; keep
			// the eggs in one basket (an error event would twin it).
			a.log("info", "stt", "offline STT install cancelled: %s", req.ModelID)
			return
		}
		a.log("error", "stt", "offline STT install failed: %v", err)
		a.Bus.Emit(contracts.EventSTTInstallError, contracts.STTInstallProgressDTO{
			ModelID: req.ModelID, Message: err.Error(),
		})
	}()
	return contracts.STTInstallStartResult{Started: true, Running: true}, nil
}

// handleSTTSettingsInstallCancel stops the in-flight install synchronously:
// it cancels the context, hands the worker a small grace window to finish,
// then reports the resulting state. Single-flight + done-channel keep the
// race window deterministic regardless of ordering.
func (a *App) handleSTTSettingsInstallCancel() (any, *contracts.RPCError) {
	a.sttInstallMu.Lock()
	if !a.sttInstallActive {
		a.sttInstallMu.Unlock()
		return contracts.STTInstallStartResult{Running: false}, nil
	}
	cancel := a.sttInstallCancel
	done := a.sttInstallDoneCh
	if cancel != nil {
		cancel()
		a.sttInstallCancel = nil
	}
	a.sttInstallActive = false // the window is being stopped; surface it
	a.sttInstallMu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	}
	return contracts.STTInstallStartResult{Running: false, Message: "install cancelled"}, nil
}

// sttInstallEnd releases the single-flight slot and signals waiters.
func (a *App) sttInstallEnd() {
	a.sttInstallMu.Lock()
	defer a.sttInstallMu.Unlock()
	if a.sttInstallActive {
		a.sttInstallActive = false
	}
	if a.sttInstallDoneCh != nil {
		close(a.sttInstallDoneCh)
		a.sttInstallDoneCh = nil
	}
	a.sttInstallCancel = nil
}

// sttInstallRunning reports the slot from the App's perspective.
func (a *App) sttInstallRunning() bool {
	a.sttInstallMu.Lock()
	defer a.sttInstallMu.Unlock()
	return a.sttInstallActive
}

// sttInstallBegin claims the single-flight slot.
func (a *App) sttInstallBegin() bool {
	a.sttInstallMu.Lock()
	defer a.sttInstallMu.Unlock()
	if a.sttInstallActive {
		return false
	}
	a.sttInstallActive = true
	return true
}

// knownSTTModel maintains catalog visibility without pulling the
// infrastructure type into application.
func knownSTTModel(id string) bool {
	for _, v := range contracts.OfflineSTTModelIDs {
		if v == id {
			return true
		}
	}
	return false
}
