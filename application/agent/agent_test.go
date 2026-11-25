package appagent

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoagent "github.com/jahrulnr/nusashell/infrastructure/repository/agent"
)

// stubRuntime answers the agent's options and records what it is told to
// start; the rest of the port is not under test here.
type stubRuntime struct {
	dservice.AgentRuntime
	options []dservice.AgentOption
	probe   error

	mu     sync.Mutex
	warmed []string
}

func (s *stubRuntime) Options(context.Context, *aggregate.Agent, string) ([]dservice.AgentOption, dservice.OptionsState, error) {
	return s.options, dservice.OptionsReady, s.probe
}
func (s *stubRuntime) Reload(string) {}
func (s *stubRuntime) Warm(_ context.Context, a *aggregate.Agent) error {
	s.mu.Lock()
	s.warmed = append(s.warmed, a.ID())
	s.mu.Unlock()
	return nil
}
func (s *stubRuntime) warmedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.warmed...)
}

func fixture(t *testing.T) (*AgentApp, *stubRuntime) {
	t.Helper()
	rt := &stubRuntime{options: []dservice.AgentOption{
		{ID: "mode", Name: "Mode", Type: "select", Choices: []dservice.OptionChoice{{Value: "plan"}, {Value: "build"}}},
		{ID: "fast", Name: "Fast", Type: "boolean"},
	}}
	return New(repoagent.New(t.TempDir(), nil), rt, nil, nil), rt
}

func save(t *testing.T, app *AgentApp, in Input) error {
	t.Helper()
	if in.ID == "" {
		in.ID = "claude"
	}
	if in.Command == "" {
		in.Command = "claude-agent-acp"
	}
	_, err := app.Upsert(context.Background(), in)
	return err
}

func TestDefaultsAreCheckedAgainstWhatTheAgentOffers(t *testing.T) {
	app, _ := fixture(t)
	// The agent has to exist before it can be asked.
	if err := save(t, app, Input{}); err != nil {
		t.Fatal(err)
	}
	for name, defaults := range map[string]valueobject.ConfigSelections{
		"unknown option": {{ID: "theme", Value: "dark"}},
		"unknown choice": {{ID: "mode", Value: "yolo"}},
		"not a boolean":  {{ID: "fast", Value: "maybe"}},
	} {
		err := save(t, app, Input{Defaults: defaults})
		if err == nil || apperror.As(err).StatusCode() != 400 {
			t.Errorf("%s: err = %v, want a 400", name, err)
		}
	}
	if err := save(t, app, Input{Defaults: valueobject.ConfigSelections{{ID: "mode", Value: "plan"}, {ID: "fast", Value: "true"}}}); err != nil {
		t.Fatalf("valid defaults refused: %v", err)
	}
	v, err := app.Get(context.Background(), "claude")
	if err != nil || len(v.Defaults) != 2 || v.Defaults[0].ID != "mode" || v.Defaults[0].Value != "plan" || v.Defaults[1].ID != "fast" || v.Defaults[1].Value != "true" {
		t.Fatalf("view = %+v, %v", v, err)
	}
}

func TestDefaultsNeedTheAgentToAnswer(t *testing.T) {
	app, rt := fixture(t)
	if err := save(t, app, Input{}); err != nil {
		t.Fatal(err)
	}
	rt.probe = apperror.New(502, "agent failed to start", nil)
	if err := save(t, app, Input{Defaults: valueobject.ConfigSelections{{ID: "mode", Value: "plan"}}}); err == nil || apperror.As(err).StatusCode() != 502 {
		t.Fatalf("err = %v, want a 502", err)
	}
	// Clearing defaults asks nothing of the agent.
	if err := save(t, app, Input{}); err != nil {
		t.Fatalf("saving without defaults failed: %v", err)
	}
}

func TestResidentAgentsStartAtOnce(t *testing.T) {
	app, rt := fixture(t)
	if err := save(t, app, Input{AutoStart: true}); err != nil {
		t.Fatal(err)
	}
	if err := save(t, app, Input{ID: "codex", Command: "codex-acp"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(rt.warmedIDs()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := rt.warmedIDs(); len(got) != 1 || got[0] != "claude" {
		t.Fatalf("warmed = %v, want only the resident agent", got)
	}

	// The server's start warms the resident ones too.
	app.WarmResident(context.Background())
	deadline = time.Now().Add(2 * time.Second)
	for len(rt.warmedIDs()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := rt.warmedIDs(); len(got) != 2 || got[1] != "claude" {
		t.Fatalf("warmed = %v", got)
	}
	v, _ := app.Get(context.Background(), "claude")
	if !v.AutoStart {
		t.Fatal("the view does not say the agent is resident")
	}
}

// A store failure used to leave WarmResident silent — the Agents page broke
// with nothing in the log. The error must reach the diagnostic log.
func TestWarmResidentReportsAListFailure(t *testing.T) {
	var buf bytes.Buffer
	repo := failingAgentRepo{err: errors.New("store down")}
	app := New(repo, &stubRuntime{}, nil, slog.New(slog.NewTextHandler(&buf, nil)))
	app.WarmResident(context.Background())
	if got := buf.String(); !strings.Contains(got, "resident agents could not be listed") ||
		!strings.Contains(got, "store down") {
		t.Fatalf("log = %q, want the List failure reported", got)
	}
}

type failingAgentRepo struct {
	drepo.Agent
	err error
}

func (r failingAgentRepo) List(context.Context) ([]aggregate.Agent, error) {
	return nil, r.err
}
