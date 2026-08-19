package ci

import (
	"path/filepath"

	"nusashell/application"
)

// BuildAutomation wires durable stores, the local executor, and both schedulers.
func BuildAutomation(dataDir string, bus *application.Bus, plugins application.PluginStore, mcp application.MCPToolbox, caller application.MCPToolCaller) (*application.Automation, *SQLite, error) {
	store, err := OpenSQLite(filepath.Join(dataDir, "ci", "automation.db"))
	if err != nil {
		return nil, nil, err
	}
	caps := application.NewCapabilityRegistry()
	caps.Plugins = plugins
	caps.MCP = mcp
	caps.Caller = caller
	caps.State = ProviderStateSQL{store}
	caps.Workflows = WorkflowSQL{store}
	es := application.NewExecutionScheduler()
	es.Runs = RunSQL{store}
	es.Logs = LogSQL{store}
	es.Exec = &LocalExecutor{Root: filepath.Join(dataDir, "ci", "runs")}
	es.Caps = caps
	es.Waits = WaitSQL{store}
	es.Clock = application.SystemClock{}
	es.Bus = bus
	es.Notifier = NewHTTPNotifier()
	autoSched := &application.AutomationScheduler{
		Workflows: WorkflowSQL{store},
		Schedules: ScheduleSQL{store},
		Events:    EventSQL{store},
		Waits:     WaitSQL{store},
		Locks:     LockSQL{store},
		Debounce:  DebounceSQL{store},
		Caps:      caps,
		Exec:      es,
		Clock:     application.SystemClock{},
		Bus:       bus,
	}
	svc := &application.Automation{
		ParseYAML: ParseYAML,
		Files:     FilePipelineStore{},
		Workflows: WorkflowSQL{store},
		Runs:      RunSQL{store},
		Schedules: ScheduleSQL{store},
		Events:    EventSQL{store},
		Exec:      es,
		Auto:      autoSched,
		Caps:      caps,
		Logs:      LogSQL{store},
		Clock:     application.SystemClock{},
	}
	return svc, store, nil
}
