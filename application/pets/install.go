package pets

import (
	"context"
	"runtime"

	"nusashell/contracts"
)

func (s *Service) HandleStatus() (any, *contracts.RPCError) {
	res := contracts.PetsStatusResult{Supported: runtime.GOOS == "linux"}
	if s.installer != nil {
		res = s.installer.Status()
		res.Supported = res.Supported && runtime.GOOS == "linux"
	}
	res.InstallActive = s.InstallRunning()
	return res, nil
}

func (s *Service) HandleInstallStart(req contracts.PetsInstallStartRequest) (any, *contracts.RPCError) {
	if s.installer == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "desktop pet installer is not available in this build"}
	}
	if !s.installer.Status().Supported {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "desktop pet is supported on Linux only"}
	}
	if !s.beginInstall() {
		return contracts.PetsInstallStartResult{Started: false, Running: true, Message: "a desktop pet install is already running"}, nil
	}

	s.goFn(func() {
		defer s.endInstall()
		err := s.installer.Install(context.Background(), req.Version, func(p contracts.PetsInstallProgressDTO) {
			s.emit(contracts.EventPetsInstallProgress, p)
		})
		if err != nil {
			s.write("error", "desktop pet install failed: %v", err)
			s.emit(contracts.EventPetsInstallError, contracts.PetsInstallProgressDTO{Message: err.Error()})
			return
		}
		s.write("info", "desktop pet installed: version=%q", req.Version)
		s.emit(contracts.EventPetsInstallDone, contracts.PetsInstallProgressDTO{
			Phase:   "verify",
			Message: "Desktop pet ready",
		})
	})
	return contracts.PetsInstallStartResult{Started: true, Running: true}, nil
}

func (s *Service) HandleLaunch() (any, *contracts.RPCError) {
	if s.installer == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "desktop pet installer is not available in this build"}
	}
	status := s.installer.Status()
	if !status.Supported {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "desktop pet is supported on Linux only"}
	}
	if s.InstallRunning() {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "a desktop pet install is currently running; launch is paused"}
	}
	if !status.Installed || status.Path == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "desktop pet is not installed"}
	}
	if status.Running {
		if err := s.installer.Stop(); err != nil {
			s.write("warn", "stop failed: %v", err)
			return contracts.PetsLaunchResult{Message: err.Error()}, nil
		}
		s.write("info", "stopped desktop pet: %s", status.Path)
		return contracts.PetsLaunchResult{Stopped: true, Path: status.Path}, nil
	}
	path, err := s.installer.Launch()
	if err != nil {
		s.write("warn", "launch failed: %v", err)
		return contracts.PetsLaunchResult{Launched: false, Message: err.Error()}, nil
	}
	s.write("info", "launched desktop pet: %s", path)
	return contracts.PetsLaunchResult{Launched: true, Path: path}, nil
}
