package ci

import (
	"os"
	"path/filepath"

	"nusashell/application"
)

// BuildCI wires durable stores, the local executor, and both schedulers.
func BuildCI(dataDir string, bus *application.Bus, plugins application.PluginStore, mcp application.MCPToolbox, caller application.MCPToolCaller) (*application.CI, *SQLite, error) {
	dbPath := filepath.Join(dataDir, "ci", "workflows.db")
	migrateAutomationDB(dataDir, dbPath)
	store, err := OpenSQLite(dbPath)
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
	autoSched := &application.CIScheduler{
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
	svc := &application.CI{
		ParseYAML: ParseYAML,
		Pipelines: DirPipelineStore{Root: filepath.Join(dataDir, PipelinesDir)},
		Workflows: WorkflowSQL{store},
		Runs:      RunSQL{store},
		Schedules: ScheduleSQL{store},
		Events:    EventSQL{store},
		Exec:      es,
		Sched:     autoSched,
		Caps:      caps,
		Logs:      LogSQL{store},
		Clock:     application.SystemClock{},
	}
	return svc, store, nil
}

// migrateAutomationDB renames the legacy <dataDir>/ci/automation.db to
// <dataDir>/ci/workflows.db when the new path does not exist yet. This is a
// one-time migration so existing users keep their saved workflows after the
// automation → CI namespace unification. If both files exist, the new path
// wins (the old file is left in place for manual recovery). Errors are
// silently ignored — a failed rename surfaces as OpenSQLite creating a fresh
// empty database, which is the same behavior as a new install.
func migrateAutomationDB(dataDir, newPath string) {
	if _, err := os.Stat(newPath); err == nil || !os.IsNotExist(err) {
		return
	}
	oldPath := filepath.Join(dataDir, "ci", "automation.db")
	if _, err := os.Stat(oldPath); err != nil {
		return
	}
	_ = os.Rename(oldPath, newPath)
}
