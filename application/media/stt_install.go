package media

import (
	"context"
	"fmt"
	"time"

	"nusashell/contracts"
)

// HandleSTTInstallStatus snapshots the disk state plus the live
// install hinge. The wheel's "reason" states what to do next
// (engine → model → ping).
func (s *Service) HandleSTTInstallStatus() (any, *contracts.RPCError) {
	if s.sttInstaller == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "offline STT installer unavailable in this build"}
	}
	res := s.sttInstaller.Status()
	if s.settings != nil {
		res.ActiveModel = s.settings.Get().STTOfflineModel
	}
	res.Running = s.STTInstallRunning()
	return res, nil
}

// HandleSTTInstallStart launches one install goroutine and
// immediately returns; progress rides the Bus as stt.install.* events.
func (s *Service) HandleSTTInstallStart(req contracts.STTInstallStartRequest) (any, *contracts.RPCError) {
	if s.sttInstaller == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "offline STT installer unavailable in this build"}
	}
	if req.ModelID == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "model_id is required"}
	}
	if !knownSTTModel(req.ModelID) {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: fmt.Sprintf("unknown STT model %q", req.ModelID)}
	}
	if !s.sttInstallBegin() {
		return contracts.STTInstallStartResult{Started: false, Running: true, Message: "an offline STT install is already running"}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.sttInstallMu.Lock()
	s.sttInstallCancel = cancel
	s.sttInstallDoneCh = make(chan struct{})
	s.sttInstallMu.Unlock()
	s.goFn("stt", func() {
		defer s.sttInstallEnd()
		err := s.sttInstaller.Install(ctx, req.ModelID, func(p contracts.STTInstallProgressDTO) {
			p.ModelID = req.ModelID
			s.emit(contracts.EventSTTInstallProgress, p)
		})
		if err == nil {
			s.write("info", "stt", "offline STT installed: %s", req.ModelID)
			s.emit(contracts.EventSTTInstallDone, contracts.STTInstallProgressDTO{
				ModelID: req.ModelID, Phase: "verify", Message: "Offline STT ready",
			})
			return
		}
		if ctx.Err() == context.Canceled {
			s.write("info", "stt", "offline STT install cancelled: %s", req.ModelID)
			return
		}
		s.write("error", "stt", "offline STT install failed: %v", err)
		s.emit(contracts.EventSTTInstallError, contracts.STTInstallProgressDTO{
			ModelID: req.ModelID, Message: err.Error(),
		})
	})
	return contracts.STTInstallStartResult{Started: true, Running: true}, nil
}

// HandleSTTInstallCancel stops the in-flight install synchronously:
// it cancels the context, hands the worker a small grace window to finish,
// then reports the resulting state. Single-flight + done-channel keep the
// race window deterministic regardless of ordering.
func (s *Service) HandleSTTInstallCancel() (any, *contracts.RPCError) {
	s.sttInstallMu.Lock()
	if !s.sttInstallActive {
		s.sttInstallMu.Unlock()
		return contracts.STTInstallStartResult{Running: false}, nil
	}
	cancel := s.sttInstallCancel
	done := s.sttInstallDoneCh
	if cancel != nil {
		cancel()
		s.sttInstallCancel = nil
	}
	s.sttInstallActive = false
	s.sttInstallMu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	}
	return contracts.STTInstallStartResult{Running: false, Message: "install cancelled"}, nil
}

func (s *Service) sttInstallEnd() {
	s.sttInstallMu.Lock()
	defer s.sttInstallMu.Unlock()
	if s.sttInstallActive {
		s.sttInstallActive = false
	}
	if s.sttInstallDoneCh != nil {
		close(s.sttInstallDoneCh)
		s.sttInstallDoneCh = nil
	}
	s.sttInstallCancel = nil
}

// STTInstallRunning reports the single-flight install slot.
func (s *Service) STTInstallRunning() bool {
	s.sttInstallMu.Lock()
	defer s.sttInstallMu.Unlock()
	return s.sttInstallActive
}

func (s *Service) sttInstallBegin() bool {
	s.sttInstallMu.Lock()
	defer s.sttInstallMu.Unlock()
	if s.sttInstallActive {
		return false
	}
	s.sttInstallActive = true
	return true
}

func knownSTTModel(id string) bool {
	for _, v := range contracts.OfflineSTTModelIDs {
		if v == id {
			return true
		}
	}
	return false
}
