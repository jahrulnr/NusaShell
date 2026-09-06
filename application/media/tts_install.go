package media

import (
	"context"
	"fmt"

	"nusashell/contracts"
)

// HandleTTSInstallStatus snapshots what is installed on disk plus
// whether an install is currently running.
func (s *Service) HandleTTSInstallStatus() (any, *contracts.RPCError) {
	res := contracts.TTSInstallStatusResult{}
	if s.ttsInstaller != nil {
		res = s.ttsInstaller.Status()
	}
	res.Running = s.TTSInstallRunning()
	return res, nil
}

// HandleTTSInstallStart kicks off one offline TTS install in the
// background. Progress flows to the UI via tts.install.* Bus events; the
// RPC returns immediately so the dialog can render live progress.
func (s *Service) HandleTTSInstallStart(req contracts.TTSInstallStartRequest) (any, *contracts.RPCError) {
	if s.ttsInstaller == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "offline TTS installer is not available in this build"}
	}
	if req.VoiceID == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "voice_id is required"}
	}
	if !knownTTSVoice(req.VoiceID) {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: fmt.Sprintf("unknown voice %q", req.VoiceID)}
	}
	s.ttsInstallMu.Lock()
	begin := !s.ttsInstallActive
	if begin {
		s.ttsInstallActive = true
	}
	s.ttsInstallMu.Unlock()
	if !begin {
		return contracts.TTSInstallStartResult{Started: false, Running: true, Message: "an offline TTS install is already running"}, nil
	}

	ctx := context.Background()
	s.goFn("tts", func() {
		defer func() {
			s.ttsInstallMu.Lock()
			s.ttsInstallActive = false
			s.ttsInstallMu.Unlock()
		}()
		err := s.ttsInstaller.Install(ctx, req.VoiceID, func(p contracts.TTSInstallProgressDTO) {
			p.VoiceID = req.VoiceID // stamp once here; adapters fill phase/bytes only
			s.emit(contracts.EventTTSInstallProgress, p)
		})
		if err != nil {
			s.write("error", "tts", "offline TTS install failed: %v", err)
			s.emit(contracts.EventTTSInstallError, contracts.TTSInstallProgressDTO{
				VoiceID: req.VoiceID, Message: err.Error(),
			})
			return
		}
		s.write("info", "tts", "offline TTS installed: %s", req.VoiceID)
		s.emit(contracts.EventTTSInstallDone, contracts.TTSInstallProgressDTO{
			VoiceID: req.VoiceID, Phase: "verify", Message: "Offline TTS ready",
		})
	})
	return contracts.TTSInstallStartResult{Started: true, Running: true}, nil
}

// TTSInstallRunning reports the single-flight install slot.
func (s *Service) TTSInstallRunning() bool {
	s.ttsInstallMu.Lock()
	defer s.ttsInstallMu.Unlock()
	return s.ttsInstallActive
}

func knownTTSVoice(id string) bool {
	for _, v := range contracts.OfflineTTSVoiceIDs {
		if v == id {
			return true
		}
	}
	return false
}
