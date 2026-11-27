package enum

// Runtime identifies who runs a conversation's turns. The harness runs
// NusaShell's provider-call loop. An agent runs inference and tools through
// its own session. A new kind is added here, on the matrix methods, and
// nowhere else.
type Runtime string

const (
	RuntimeHarness Runtime = "harness"
	RuntimeAgent   Runtime = "agent"
)

func (r Runtime) String() string { return string(r) }

// UsesProviderRoute is provider and model resolution. The harness resolves
// a provider route; an agent does not.
func (r Runtime) UsesProviderRoute() bool { return r == RuntimeHarness }

// AcceptsProviderPicks is effort and upstream. They are settings of a
// provider call. An agent takes neither.
func (r Runtime) AcceptsProviderPicks() bool { return r == RuntimeHarness }

// HarnessOwnsContext is native compaction and the footer's harness
// estimate. Usage accounting is not this rule: an agent turn records
// usage too.
func (r Runtime) HarnessOwnsContext() bool { return r == RuntimeHarness }

// MirrorsAgentCommands is the agent's command surface. A harness room
// carries NusaShell's own commands.
func (r Runtime) MirrorsAgentCommands() bool { return r == RuntimeAgent }

// QueriesAgentSessionOptions is the agent's session-option catalog and
// the picks stored against it.
func (r Runtime) QueriesAgentSessionOptions() bool { return r == RuntimeAgent }

// ReadsAgentLaunchEnvironment is the bound agent's launch environment,
// including typed [[env:NAME]] resolution.
func (r Runtime) ReadsAgentLaunchEnvironment() bool { return r == RuntimeAgent }

// RequiresLocalExecution is where the kind may run. An agent process is
// local; the harness drives a provider from any environment.
func (r Runtime) RequiresLocalExecution() bool { return r == RuntimeAgent }
