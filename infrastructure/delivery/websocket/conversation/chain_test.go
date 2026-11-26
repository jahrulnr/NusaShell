package wsconversation

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoconversation "github.com/jahrulnr/nusashell/infrastructure/repository/conversation"
	repoprovider "github.com/jahrulnr/nusashell/infrastructure/repository/provider"
	reposecret "github.com/jahrulnr/nusashell/infrastructure/repository/secret"
	coretext "github.com/jahrulnr/nusashell/infrastructure/service/core/text"
	textcompletion "github.com/jahrulnr/nusashell/infrastructure/service/core/text/completion"
)

// The B-068 chain test: a turn running without a socket (the queued-job
// shape) is canceled through the hub, and the proof is the job's end —
// 408 canceled — plus a terminal frame in the journal. Not the ack, not
// the return value: the job dies.
func TestCancelThroughTheHubEndsTheWorkerJob(t *testing.T) {
	// A provider that never answers in time: the turn stays running until
	// the interruption abandons the upstream call. The handler blocks on a
	// channel, not the request ctx — canceling the job abandons the call
	// without necessarily severing the connection, and a handler waiting
	// on the ctx would hold httptest.Close forever.
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	entered := make(chan struct{})
	var enteredOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enteredOnce.Do(func() { close(entered) })
		<-release
	}))
	t.Cleanup(srv.Close)
	prov, _ := aggregate.NewProvider("gw1", "stub", srv.URL, enum.StyleCompletion)
	prov.SetApiKey("sk-test")
	prov.DisableStream()
	m, _ := entity.NewModel("m1")
	prov.SetModels([]entity.Model{m})
	secrets, err := reposecret.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { secrets.Close() })
	providers := repoprovider.NewProviderRepository(t.TempDir(), secrets)
	if err := providers.Save(t.Context(), &prov); err != nil {
		t.Fatal(err)
	}
	app := appconversation.New(
		repoconversation.NewConversationRepository(t.TempDir()),
		providers,
		map[enum.Environment]appjob.EnvironmentDriver{enum.EnvironmentLocal: appjob.EnvironmentBundle{
			DefaultRoot: t.TempDir(),
			Services:    map[enum.Style]dservice.TextModelService{enum.StyleCompletion: textcompletion.New(coretext.Options{})},
		}},
		config.NewConfig(), nil, "", nil, nil,
	)
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

	h := newHub(t, stubSender{})
	// The serve.go wiring, replicated exactly: the app journals through the
	// hub, the hub reaches the worker through the app — one direction.
	app.UseTurnSurface(h)
	h.UseJobControl(
		func(convID string) bool { return app.Cancel(context.Background(), convID) == nil },
		nil,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		// A send with no event handler — the shape a queued job executes
		// with: the journal is the live surface's, opened at execution.
		_, err := app.Send(ctx, "c_chain", "gw1", "m1", "work that never ends", nil, appjob.SendOptions{}, nil)
		done <- err
	}()

	// The turn must be running — upstream entered — and journaled before
	// the cancel, or the test proves nothing.
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the turn never reached the upstream")
	}
	var tid string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if id, _, ok := h.ActiveTurn("c_chain"); ok {
			tid = id
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if tid == "" {
		t.Fatal("the turn never appeared in the live registry")
	}

	if !h.Cancel("c_chain") {
		t.Fatal("stopped=false while a live worker was running the job")
	}

	select {
	case err := <-done:
		if apperror.As(err).StatusCode() != 408 {
			t.Fatalf("job end = %v, want 408 canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the job never ended — the cancel reached nothing")
	}

	// The journal must carry the terminal: a watcher reloading the room
	// reads the outcome instead of a turn that looks open forever.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		jt, ok := h.turns[tid]
		terminal := ok && jt.terminal
		h.mu.Unlock()
		if terminal {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.mu.Lock()
	jt := h.turns[tid]
	h.mu.Unlock()
	if jt == nil || !jt.terminal {
		t.Fatal("the journal never sealed — the turn looks open forever")
	}
	sawTerminal := false
	for _, f := range jt.frames {
		if bytes.Contains(f.raw, []byte(`"type":"turn.failed"`)) || bytes.Contains(f.raw, []byte(`"type":"turn.completed"`)) {
			sawTerminal = true
		}
	}
	if !sawTerminal {
		t.Fatal("the journal holds no terminal frame")
	}
}
