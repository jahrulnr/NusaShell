// Package appmcp manages the owner's MCP servers. Every agent session and
// the model runtime get the enabled ones; a change reaches the model
// runtime at once and agents at their next session.
package appmcp

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Runtime is the model runtime's side: it connects to the enabled
// servers and reports how each is doing.
type Runtime interface {
	Load(ctx context.Context, servers []aggregate.MCPServer)
	Status() map[string]Status
}

// Status is how the model runtime's connection to a server is doing.
type Status struct {
	Connected bool   `json:"connected"`
	Tools     int    `json:"tools"`
	Error     string `json:"error,omitempty"`
}

// App is the use case.
type App struct {
	repo    drepo.MCPServer
	runtime Runtime
	log     *slog.Logger
	now     func() time.Time
}

// New wires the use case. runtime may be nil (the CLI).
func New(repo drepo.MCPServer, runtime Runtime, log *slog.Logger) *App {
	if log == nil {
		log = slog.Default()
	}
	return &App{repo: repo, runtime: runtime, log: log.With("component", "mcp"), now: time.Now}
}

// Pair is one environment variable or header. An empty Value on an edit
// keeps the stored value, so a form need not show secrets.
type Pair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Input is a server as the owner enters it.
type Input struct {
	Name      string   `json:"name"`
	Transport string   `json:"transport"`
	Command   string   `json:"command"`
	Args      []string `json:"args"`
	Env       []Pair   `json:"env"`
	URL       string   `json:"url"`
	Headers   []Pair   `json:"headers"`
	Enabled   *bool    `json:"enabled"`
}

// View is a server as shown: values of variables and headers stay hidden.
type View struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Transport string   `json:"transport"`
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
	Env       []string `json:"env,omitempty"`
	URL       string   `json:"url,omitempty"`
	Headers   []string `json:"headers,omitempty"`
	Enabled   bool     `json:"enabled"`
	Status    *Status  `json:"status,omitempty"`
	CreatedAt string   `json:"createdAt"`
	UpdatedAt string   `json:"updatedAt"`
}

func (a *App) view(s aggregate.MCPServer, status map[string]Status) View {
	v := View{ID: s.ID(), Name: s.Name(), Transport: s.Transport().String(), Command: s.Command(), Args: s.Args(),
		Env: keys(s.Env()), URL: s.URL(), Headers: keys(s.Headers()), Enabled: s.Enabled(),
		CreatedAt: s.CreatedAt().UTC().Format(time.RFC3339Nano), UpdatedAt: s.UpdatedAt().UTC().Format(time.RFC3339Nano)}
	if st, ok := status[s.Name()]; ok && s.Enabled() {
		v.Status = &st
	}
	return v
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (a *App) status() map[string]Status {
	if a.runtime == nil {
		return nil
	}
	return a.runtime.Status()
}

// List returns every server by name.
func (a *App) List(ctx context.Context) ([]View, error) {
	list, err := a.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	st := a.status()
	out := make([]View, 0, len(list))
	for _, s := range list {
		out = append(out, a.view(s, st))
	}
	return out, nil
}

// Get returns one server.
func (a *App) Get(ctx context.Context, id string) (View, error) {
	s, err := a.repo.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	return a.view(*s, a.status()), nil
}

// Find resolves a server by id or name.
func (a *App) Find(ctx context.Context, ref string) (View, error) {
	list, err := a.repo.List(ctx)
	if err != nil {
		return View{}, err
	}
	for _, s := range list {
		if s.ID() == ref || s.Name() == ref {
			return a.view(s, a.status()), nil
		}
	}
	return View{}, apperror.NewNotFoundError("MCP server "+ref, nil)
}

// Create adds a server.
func (a *App) Create(ctx context.Context, in Input) (View, error) {
	now := a.now()
	s, err := aggregate.NewMCPServer("", in.Name, now)
	if err != nil {
		return View{}, apperror.New(400, err.Error(), err)
	}
	if err := apply(s, in, nil, nil); err != nil {
		return View{}, err
	}
	if err := a.repo.Create(ctx, s); err != nil {
		return View{}, err
	}
	a.Reload(ctx)
	return a.view(*s, a.status()), nil
}

// Update edits a server. readAt is the updatedAt the edit started from.
func (a *App) Update(ctx context.Context, id string, in Input, readAt time.Time) (View, error) {
	s, err := a.repo.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	stored := s.UpdatedAt()
	if !readAt.IsZero() && !readAt.Equal(stored) {
		return View{}, apperror.New(409, "the MCP server was changed elsewhere — reload it and try again", aggregate.ErrMCPServerChanged)
	}
	if in.Name != "" && in.Name != s.Name() {
		if err := s.Rename(in.Name); err != nil {
			return View{}, apperror.New(400, err.Error(), err)
		}
	}
	if err := apply(s, in, s.Env(), s.Headers()); err != nil {
		return View{}, err
	}
	s.Touch(later(a.now(), stored))
	if err := a.repo.Update(ctx, s, stored); err != nil {
		return View{}, err
	}
	a.Reload(ctx)
	return a.view(*s, a.status()), nil
}

// SetEnabled turns a server on or off.
func (a *App) SetEnabled(ctx context.Context, id string, on bool) (View, error) {
	s, err := a.repo.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	stored := s.UpdatedAt()
	s.SetEnabled(on)
	s.Touch(later(a.now(), stored))
	if err := a.repo.Update(ctx, s, stored); err != nil {
		return View{}, err
	}
	a.Reload(ctx)
	return a.view(*s, a.status()), nil
}

// Delete removes a server.
func (a *App) Delete(ctx context.Context, id string) error {
	if err := a.repo.Delete(ctx, id); err != nil {
		return err
	}
	a.Reload(ctx)
	return nil
}

// Reload hands the enabled servers to the model runtime.
func (a *App) Reload(ctx context.Context) {
	if a.runtime == nil {
		return
	}
	list, err := a.enabled(ctx)
	if err != nil {
		a.log.Warn("MCP servers not loaded", "error", err)
		return
	}
	a.runtime.Load(ctx, list)
}

func (a *App) enabled(ctx context.Context) ([]aggregate.MCPServer, error) {
	list, err := a.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(list, func(s aggregate.MCPServer) bool { return !s.Enabled() }), nil
}

// ForAgents lists the enabled servers as an agent launches them.
func (a *App) ForAgents(ctx context.Context) []dservice.MCPServer {
	list, err := a.enabled(ctx)
	if err != nil {
		a.log.Warn("MCP servers not offered to agents", "error", err)
		return nil
	}
	out := make([]dservice.MCPServer, 0, len(list))
	for _, s := range list {
		out = append(out, dservice.MCPServer{Name: s.Name(), Transport: s.Transport(), Command: s.Command(), Args: s.Args(),
			Env: s.Env(), URL: s.URL(), Headers: s.Headers()})
	}
	return out
}

// apply sets the launch from in. oldEnv and oldHeaders fill values an edit
// left empty.
func apply(s *aggregate.MCPServer, in Input, oldEnv, oldHeaders map[string]string) error {
	t, ok := enum.ParseMCPTransport(strings.TrimSpace(in.Transport))
	if !ok {
		return apperror.New(400, "transport must be stdio, http, or sse", nil)
	}
	var err error
	if t.Remote() {
		err = s.SetRemote(t, in.URL, pairs(in.Headers, oldHeaders))
	} else {
		err = s.SetStdio(in.Command, in.Args, pairs(in.Env, oldEnv))
	}
	if err != nil {
		return apperror.New(400, err.Error(), err)
	}
	if in.Enabled != nil {
		s.SetEnabled(*in.Enabled)
	}
	return nil
}

func pairs(in []Pair, old map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for _, p := range in {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			continue
		}
		if p.Value == "" {
			out[name] = old[name]
			continue
		}
		out[name] = p.Value
	}
	return out
}

// later keeps updatedAt moving forward, so an edit within the clock's
// resolution still changes the conflict token.
func later(now, prev time.Time) time.Time {
	if now.After(prev) {
		return now
	}
	return prev.Add(time.Nanosecond)
}

// Watch reloads the model runtime when the stored servers change outside
// this process (the CLI), checking every interval until ctx ends.
func (a *App) Watch(ctx context.Context, every time.Duration) {
	last := a.signature(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if sig := a.signature(ctx); sig != last {
			last = sig
			a.Reload(ctx)
		}
	}
}

func (a *App) signature(ctx context.Context) string {
	list, err := a.repo.List(ctx)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, s := range list {
		fmt.Fprintf(&b, "%s/%d/%t;", s.ID(), s.UpdatedAt().UnixNano(), s.Enabled())
	}
	return b.String()
}
