package enum

// AutomationThread says which thread an automation's runs land in.
type AutomationThread string

const (
	// AutomationThreadNew opens a new thread for every run.
	AutomationThreadNew AutomationThread = "new"
	// AutomationThreadSame keeps one thread for the automation; each run
	// continues it, so the runtime sees the earlier runs.
	AutomationThreadSame AutomationThread = "same"
)

func (t AutomationThread) String() string { return string(t) }

func ParseAutomationThread(v string) (AutomationThread, bool) {
	switch t := AutomationThread(v); t {
	case AutomationThreadNew, AutomationThreadSame:
		return t, true
	}
	return "", false
}

// RunStatus is where one automation run stands.
type RunStatus string

const (
	RunRunning   RunStatus = "running"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
	RunCancelled RunStatus = "cancelled"
	// RunInterrupted marks a run the server stopped under: its turn ended
	// with the process, so whether it finished is unknown.
	RunInterrupted RunStatus = "interrupted"
)

func (s RunStatus) String() string { return string(s) }

func ParseRunStatus(v string) (RunStatus, bool) {
	switch s := RunStatus(v); s {
	case RunRunning, RunCompleted, RunFailed, RunCancelled, RunInterrupted:
		return s, true
	}
	return "", false
}

// Terminal reports whether the run has ended.
func (s RunStatus) Terminal() bool { return s != RunRunning }
