// Package appagent is the management plane for external agents:
// registration, edits, removal, and discovery of agent CLIs installed on
// the host. Turns on an agent run through appconversation; this package
// owns the agents themselves.
//
// Secret rule: environment values are write-only. Views name each variable
// and say whether its value is a reference or stored secret, never the
// value. An update that sends a variable's stored ref back keeps it.
package appagent

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// AgentApp fronts the agent store, the runtime that must forget a changed
// agent's process, and the host detector. mu serializes read-modify-write.
type AgentApp struct {
	agents   drepo.Agent
	runtime  dservice.AgentRuntime
	detector dservice.AgentDetector
	shells   dservice.Shells
	log      *slog.Logger
	mu       sync.Mutex
}

// New wires the management plane. runtime and detector may be nil: a CLI
// process has no running agents to reload, and a test may skip discovery.
func New(agents drepo.Agent, runtime dservice.AgentRuntime, detector dservice.AgentDetector, log *slog.Logger) *AgentApp {
	if log == nil {
		log = slog.Default()
	}
	return &AgentApp{agents: agents, runtime: runtime, detector: detector, log: log.With("component", "agents")}
}

// EnvVar is one launch variable in a view: its name and how its value is
// held, never the value itself.
type EnvVar struct {
	Name string `json:"name"`
	// Ref is the stored reference — "env:NAME" or "secret:<key>".
	Ref string `json:"ref"`
}

// View is the sanitized read of one agent.
type View struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Env     []EnvVar `json:"env"`
	Policy  string   `json:"policy"`
	// AutoStart keeps the agent's process running from the server's start.
	AutoStart bool `json:"autoStart"`
	// Defaults are the session options a room starts with, in apply order.
	Defaults valueobject.ConfigSelections `json:"defaults"`
}

func view(a aggregate.Agent) View {
	v := View{ID: a.ID(), Name: a.Name(), Command: a.Command(), Args: a.Args(), Policy: a.Policy().String(), Env: []EnvVar{},
		AutoStart: a.AutoStart(), Defaults: a.Defaults()}
	if v.Defaults == nil {
		v.Defaults = valueobject.ConfigSelections{}
	}
	if v.Args == nil {
		v.Args = []string{}
	}
	for name, ref := range a.Env() {
		if !valueobject.IsKeyRef(ref) {
			ref = "" // a literal that never reached the vault stays hidden
		}
		v.Env = append(v.Env, EnvVar{Name: name, Ref: ref})
	}
	slices.SortFunc(v.Env, func(x, y EnvVar) int { return strings.Compare(x.Name, y.Name) })
	return v
}

// List returns every configured agent.
func (app *AgentApp) List(ctx context.Context) ([]View, error) {
	agents, err := app.agents.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(agents))
	for _, a := range agents {
		out = append(out, view(a))
	}
	return out, nil
}

// Get returns one agent.
func (app *AgentApp) Get(ctx context.Context, id string) (View, error) {
	a, err := app.agents.Inspect(ctx, id)
	if err != nil {
		return View{}, err
	}
	return view(*a), nil
}

// Input is a full agent definition for Upsert. Env values are literal
// secrets to store, or a stored ref to keep. Policy "" means ask.
type Input struct {
	ID      string
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	Policy  string
	// AutoStart and Defaults replace the stored ones.
	AutoStart bool
	Defaults  valueobject.ConfigSelections
}

// Upsert creates or replaces an agent. The running process of an edited
// agent stops, so the next turn launches with the new definition.
func (app *AgentApp) Upsert(ctx context.Context, in Input) (View, error) {
	policy := enum.ApprovalPolicyAsk
	if in.Policy != "" {
		p, ok := enum.ParseApprovalPolicy(in.Policy)
		if !ok {
			return View{}, apperror.New(400, "unknown approval policy", nil)
		}
		policy = p
	}
	a, err := aggregate.NewAgent(strings.TrimSpace(in.ID), in.Name, in.Command, in.Args, policy)
	if err != nil {
		return View{}, apperror.New(400, "invalid agent", err)
	}
	if err := a.SetEnv(in.Env); err != nil {
		return View{}, apperror.New(400, "invalid agent environment", err)
	}
	a.SetAutoStart(in.AutoStart)
	if err := a.SetDefaults(in.Defaults); err != nil {
		return View{}, apperror.New(400, "invalid agent defaults", err)
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	if err := app.checkDefaults(ctx, a.ID(), in.Defaults); err != nil {
		return View{}, err
	}
	if err := app.agents.Save(ctx, &a); err != nil {
		return View{}, err
	}
	if app.runtime != nil {
		app.runtime.Reload(a.ID())
	}
	// A resident agent starts now rather than at its first prompt.
	if a.AutoStart() && app.runtime != nil {
		go app.warm(a.ID())
	}
	saved, err := app.agents.Inspect(ctx, a.ID())
	if err != nil {
		return View{}, err
	}
	return view(*saved), nil
}

// checkDefaults refuses defaults the agent does not offer. It asks the
// agent as it is stored, which starts it; an agent not yet stored, or a
// process with no runtime to ask (the CLI), has nothing to check against,
// and a default the agent no longer offers is ignored when a room starts.
func (app *AgentApp) checkDefaults(ctx context.Context, id string, defaults valueobject.ConfigSelections) error {
	if len(defaults) == 0 || app.runtime == nil {
		return nil
	}
	stored, err := app.agents.Load(ctx, id)
	if err != nil {
		if apperror.As(err).StatusCode() == apperror.StatusNotFound {
			return nil
		}
		return err
	}
	options, _, err := app.runtime.Options(ctx, stored, "")
	if err != nil {
		return apperror.New(502, "could not check the defaults against the agent", err)
	}
	for _, pick := range defaults {
		i := slices.IndexFunc(options, func(o dservice.AgentOption) bool { return o.ID == pick.ID })
		switch {
		case i < 0:
			return apperror.New(400, "the agent has no option "+pick.ID, nil)
		case !options[i].Offers(pick.Value):
			return apperror.New(400, "the agent does not offer "+pick.Value+" for "+pick.ID, nil)
		}
	}
	return nil
}

// warm starts a resident agent's process.
func (app *AgentApp) warm(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if a, err := app.agents.Load(ctx, id); err == nil {
		_ = app.runtime.Warm(ctx, a)
	}
}

// WarmResident starts every agent that is kept resident, as the server
// starts. An agent that fails to start is left for its first prompt, which
// reports why.
func (app *AgentApp) WarmResident(ctx context.Context) {
	if app.runtime == nil {
		return
	}
	agents, err := app.agents.List(ctx)
	if err != nil {
		app.log.Warn("resident agents could not be listed", "error", err)
		return
	}
	for _, a := range agents {
		if a.AutoStart() {
			go app.warm(a.ID())
		}
	}
}

// Delete removes an agent and stops its process. Conversations bound to
// it keep the reference and fail their next turn until it is restored.
func (app *AgentApp) Delete(ctx context.Context, id string) error {
	app.mu.Lock()
	defer app.mu.Unlock()
	if err := app.agents.Delete(ctx, id); err != nil {
		return err
	}
	if app.runtime != nil {
		app.runtime.Reload(id)
	}
	return nil
}

// Candidate is a detected agent CLI and whether an agent with its id is
// already registered.
type Candidate struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Command     string   `json:"command"`
	Args        []string `json:"args"`
	Registered  bool     `json:"registered"`
}

// Detect lists the known agent CLIs installed on the host.
func (app *AgentApp) Detect(ctx context.Context) ([]Candidate, error) {
	if app.detector == nil {
		return []Candidate{}, nil
	}
	agents, err := app.agents.List(ctx)
	if err != nil {
		return nil, err
	}
	out := []Candidate{}
	for _, c := range app.detector.Detect() {
		registered := slices.ContainsFunc(agents, func(a aggregate.Agent) bool { return a.ID() == c.ID })
		out = append(out, Candidate{ID: c.ID, Name: c.Name, Description: c.Description, Command: c.Command, Args: c.Args, Registered: registered})
	}
	return out, nil
}

// ImportDetected registers every detected CLI that has no agent yet, with
// the asking policy. It returns the agents it added.
func (app *AgentApp) ImportDetected(ctx context.Context) ([]View, error) {
	candidates, err := app.Detect(ctx)
	if err != nil {
		return nil, err
	}
	added := []View{}
	for _, c := range candidates {
		if c.Registered {
			continue
		}
		v, err := app.Upsert(ctx, Input{ID: c.ID, Name: c.Name, Command: c.Command, Args: c.Args})
		if err != nil {
			return added, err
		}
		added = append(added, v)
	}
	return added, nil
}

// Model is one model an agent offers, for the picker.
type Model struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Current bool   `json:"current"`
}

// Models asks the agent which models it offers. Starting the agent can
// take a while on first use; the runtime caches the answer until the
// agent's definition changes.
func (app *AgentApp) Models(ctx context.Context, id string) ([]Model, error) {
	if app.runtime == nil {
		return nil, apperror.New(503, "agent runtime unavailable", nil)
	}
	a, err := app.agents.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	models, err := app.runtime.Models(ctx, a)
	if err != nil {
		return nil, err
	}
	out := make([]Model, 0, len(models))
	for _, m := range models {
		out = append(out, Model{ID: m.ID, Name: m.Name, Current: m.Current})
	}
	return out, nil
}

// Options reports the session options an agent offers before any room
// exists: its modes (plan, build, bypass permissions…) and config options,
// read from the probe session the model listing already opens. A room reads
// its own through the conversation instead, because an agent may change its
// mode mid-thread and the room is what remembers it.
//
// Each agent defines its own set — the ids, names, and values are the
// agent's, not ours — so the owner's UI is generated from this rather than
// from a fixed list.
func (app *AgentApp) Options(ctx context.Context, id string) ([]dservice.AgentOption, dservice.OptionsState, error) {
	if app.runtime == nil {
		return nil, dservice.OptionsUnavailable, apperror.New(503, "agent runtime unavailable", nil)
	}
	a, err := app.agents.Load(ctx, id)
	if err != nil {
		return nil, dservice.OptionsUnavailable, err
	}
	return app.runtime.Options(ctx, a, "")
}

// AuthMethod is one way to sign the agent in. A terminal method is
// interactive: SignIn runs it in the owner's terminal, and Command is the
// same command line for a terminal of their own.
type AuthMethod struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type,omitempty"`
	Command     []string `json:"command,omitempty"`
}

// Status is the agent process as the settings page shows it.
type Status struct {
	State       string       `json:"state"`
	PID         int          `json:"pid,omitempty"`
	UptimeMs    int64        `json:"uptimeMs,omitempty"`
	LastError   string       `json:"lastError,omitempty"`
	Logs        []string     `json:"logs"`
	AuthMethods []AuthMethod `json:"authMethods"`
	AuthNeeded  bool         `json:"authNeeded,omitempty"`
	CanLogout   bool         `json:"canLogout,omitempty"`
}

// Status reports the agent's process without starting it.
func (app *AgentApp) Status(ctx context.Context, id string) (Status, error) {
	if app.runtime == nil {
		return Status{}, apperror.New(503, "agent runtime unavailable", nil)
	}
	if _, err := app.agents.Inspect(ctx, id); err != nil {
		return Status{}, err
	}
	st := app.runtime.Status(id)
	out := Status{State: string(st.State), PID: st.PID, UptimeMs: st.Uptime.Milliseconds(), LastError: st.LastError,
		Logs: st.Logs, AuthNeeded: st.AuthNeeded, CanLogout: st.CanLogout, AuthMethods: []AuthMethod{}}
	if out.Logs == nil {
		out.Logs = []string{}
	}
	for _, m := range st.AuthMethods {
		out.AuthMethods = append(out.AuthMethods, AuthMethod{ID: m.ID, Name: m.Name, Description: m.Description, Type: m.Type, Command: m.Command})
	}
	return out, nil
}

// Authenticate signs the agent in with one of its methods. Starting the
// agent for it can take a while; the caller bounds the wait.
func (app *AgentApp) Authenticate(ctx context.Context, id, methodID string) error {
	if app.runtime == nil {
		return apperror.New(503, "agent runtime unavailable", nil)
	}
	if strings.TrimSpace(methodID) == "" {
		return apperror.New(400, "methodId is required", nil)
	}
	a, err := app.agents.Load(ctx, id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	return app.runtime.Authenticate(ctx, a, methodID)
}

// UseShells sets where interactive sign-ins run.
func (app *AgentApp) UseShells(s dservice.Shells) {
	app.mu.Lock()
	app.shells = s
	app.mu.Unlock()
}

// SignIn runs one of the agent's terminal sign-in methods on a terminal
// the owner types into. When it ends the agent is stopped, so its next
// start reads the credentials the sign-in left behind.
func (app *AgentApp) SignIn(ctx context.Context, id, methodID string, cols, rows int) (dservice.Shell, error) {
	app.mu.Lock()
	shells := app.shells
	app.mu.Unlock()
	if app.runtime == nil || shells == nil {
		return nil, apperror.New(503, "terminal sign-in is unavailable", nil)
	}
	if strings.TrimSpace(methodID) == "" {
		return nil, apperror.New(400, "methodId is required", nil)
	}
	a, err := app.agents.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	start, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd, err := app.runtime.SignInCommand(start, a, methodID)
	if err != nil {
		return nil, err
	}
	sh, err := shells.Open(ctx, cmd, cols, rows)
	if err != nil {
		return nil, apperror.New(500, "could not start the sign-in command", err)
	}
	return &signIn{Shell: sh, done: func() { app.runtime.Reload(id) }}, nil
}

// signIn reloads the agent once its sign-in command has exited.
type signIn struct {
	dservice.Shell
	once sync.Once
	done func()
}

func (s *signIn) Wait() error {
	err := s.Shell.Wait()
	s.once.Do(s.done)
	return err
}

// Logout signs the agent out and stops it.
func (app *AgentApp) Logout(ctx context.Context, id string) error {
	if app.runtime == nil {
		return apperror.New(503, "agent runtime unavailable", nil)
	}
	a, err := app.agents.Load(ctx, id)
	if err != nil {
		return err
	}
	return app.runtime.Logout(ctx, a)
}

// Restart stops the agent's process; the next turn starts it again.
func (app *AgentApp) Restart(ctx context.Context, id string) error {
	if app.runtime == nil {
		return apperror.New(503, "agent runtime unavailable", nil)
	}
	if _, err := app.agents.Inspect(ctx, id); err != nil {
		return err
	}
	app.runtime.Reload(id)
	return nil
}
