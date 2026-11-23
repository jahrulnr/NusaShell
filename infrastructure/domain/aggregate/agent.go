package aggregate

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// Agent is the aggregate root for a configured external agent: a command
// that runs its own turn loop and speaks to this harness over a process
// boundary. The agent owns its tools and context; the harness owns the
// conversation record, the workspace binding, and the approval policy.
type Agent struct {
	id      string
	name    string
	command string
	args    []string
	env     map[string]string // values may be "env:"/"secret:" refs
	policy  enum.ApprovalPolicy
	// autoStart keeps the agent's process running: it starts with the
	// server and the idle reaper leaves it alone.
	autoStart bool
	// defaults are the owner's picks of the agent's session options, by
	// option id, for every room that has not picked its own.
	defaults map[string]string
}

// NewAgent validates identity, command, and policy. Name defaults to the id.
func NewAgent(id, name, command string, args []string, policy enum.ApprovalPolicy) (Agent, error) {
	a := Agent{id: id, name: strings.TrimSpace(name), command: strings.TrimSpace(command), args: slices.Clone(args), policy: policy}
	if a.name == "" {
		a.name = id
	}
	return a, a.validate()
}

func (a Agent) validate() error {
	if a.id == "" { // agents are configured, named things — never minted
		return errors.New("id is required")
	}
	if _, err := valueobject.NewID("agent", a.id); err != nil {
		return err
	}
	if a.command == "" {
		return errors.New("command is required")
	}
	if _, ok := enum.ParseApprovalPolicy(a.policy.String()); !ok {
		return fmt.Errorf("unknown approval policy %q", a.policy)
	}
	return nil
}

// SetEnv replaces the launch environment. Names must be non-empty and free
// of '='; values are literal or "env:"/"secret:" refs.
func (a *Agent) SetEnv(env map[string]string) error {
	for name := range env {
		if name == "" || strings.ContainsAny(name, "=\x00") {
			return fmt.Errorf("invalid environment variable name %q", name)
		}
	}
	a.env = maps.Clone(env)
	return nil
}

// SetAutoStart keeps the agent's process resident.
func (a *Agent) SetAutoStart(on bool) { a.autoStart = on }

// SetDefaults replaces the agent's default session options. Ids and values
// are the agent's own, so only their shape is checked here.
func (a *Agent) SetDefaults(defaults map[string]string) error {
	for id, value := range defaults {
		if strings.TrimSpace(id) == "" || len(id) > 128 || len(value) > 256 || value == "" {
			return fmt.Errorf("invalid default for option %q", id)
		}
	}
	a.defaults = maps.Clone(defaults)
	return nil
}

func (a Agent) AutoStart() bool             { return a.autoStart }
func (a Agent) Defaults() map[string]string { return maps.Clone(a.defaults) }
func (a Agent) ID() string                  { return a.id }
func (a Agent) Name() string                { return a.name }
func (a Agent) Command() string             { return a.command }
func (a Agent) Args() []string              { return slices.Clone(a.args) }
func (a Agent) Env() map[string]string      { return maps.Clone(a.env) }
func (a Agent) Policy() enum.ApprovalPolicy { return a.policy }
