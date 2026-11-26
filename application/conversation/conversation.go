// Package appconversation is the broker between callers and per-room
// workers: it owns the worker map, submits jobs, and handles worker
// lifecycle (idle eviction, delete teardown). The use-case bodies live in
// application/job — this package only routes work to a room.
//
// Concurrency model: each conversation is owned by one worker.Worker — a
// per-room serial executor. Send queues FIFO; Edit, Resume, and Delete
// preempt — they interrupt the running job and cut ahead of the wait
// line. Edit rewrites history; Resume continues it so a retry keeps the
// work the failed turn already paid for. Delete retires the worker:
// queued jobs get 410, and submits racing the teardown get ErrClosed and
// are reposted onto the fresh worker the next post spawns — a job that
// arrives after

// Shutdown rejects further posts, terminates every worker, and waits for
// their loops to exit without closing the repository.
package appconversation

import (
	"log/slog"
	"slices"
	"time"

	"context"
	"errors"
	"fmt"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	"sync"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/worker"
)

// queuedJob is what a room's worker runs: a worker.Job plus the result
// channel (Reply), room+worker binding (Bind), and preemption intent
// (IsPreempt) the broker attaches.
type queuedJob interface {
	worker.Job
	Reply(err error)
	Bind(convID string, w *worker.Worker)
	IsPreempt() bool
}

// ConversationApp is the broker. cfg is the normalized conversation
// config (mailbox size feeds the workers, turn limits ride inside deps);
// workers maps conversation id → its serial executor.
type ConversationApp struct {
	cfg         config.ConversationConfig
	configPath  string
	configStore config.Store
	deps        appjob.Deps
	mu          sync.Mutex
	workers     map[string]*worker.Worker
	// turning counts the live turns per room: the gate a pick reads before it
	// decides to apply now or wait.
	turning map[string]int
	// machineTurns counts the consecutive completion turns that spend the wake
	// budget, and wakeQueued marks a room whose wake turn is queued or running: a
	// second one would read the same records twice. The records themselves live in
	// the room's sidecar, not here — they must outlive this process.
	wakeQueued   map[string]bool
	machineTurns map[string]int
	// consumed remembers the parked pick a running turn took, by acceptedAt: the
	// projection stops offering it while that turn is in play, and EndTurn
	// releases it. A pick parked after the consumption has another acceptedAt
	// and stays visible.
	consumed map[string]time.Time

	uploads    drepo.Uploads
	picks      drepo.Picks
	recordings drepo.Recordings
	// mediaDescriptions remembers read_media's readings per room; a deleted
	// room must not leave the text it lifted from its files behind.
	mediaDescriptions dservice.MediaDescriptions
	shells            dservice.Shells
	views             drepo.ThreadViews
	// skills and mcpNames feed reference search.
	skills   drepo.Skills
	mcpNames func(context.Context) []string

	// changed hears of a room changing outside a turn (a reply's
	// delivery to a chat, a generated title), so open views reload it.
	changed []func(convID string)
	// onQueueChanged hears that a room's queue depth moved (an accepted
	// message landed, or the queue drained), so the room surface can name
	// how many wait. Nil announces nothing.
	onQueueChanged func(convID string)
	// liveTurns tells whether a transport turn is in progress in a room; the
	// hub owns that fact. Nil reads every room as idle.
	liveTurns func(convID string) bool
	// utility names the model for small tasks (thread titles); read on
	// each use, "" turns them off.
	utility func() (providerID, modelID string)

	shuttingDown bool
	shutdownDone chan struct{}
	// stopGrace bounds how long Shutdown waits for the rooms' workers to
	// unwind before naming the ones it leaves behind.
	stopGrace time.Duration
	workerWG  sync.WaitGroup
}

func New(
	convs drepo.Conversation,
	providers drepo.Provider,
	environments map[enum.Environment]appjob.EnvironmentDriver,
	cfg config.Config,
	configStore config.Store,
	configPath string,
	terminals dservice.TerminalSessions,
	summaryTool dservice.Tool,
	opts ...Option,
) *ConversationApp {
	// Normalize once at the boundary — invalid values clamp and zeros
	// pull their `default` tag, so every downstream read trusts the
	// value.
	ccfg := cfg.Conversation
	ccfg.Normalize()
	app := &ConversationApp{
		cfg:         ccfg,
		configStore: configStore,
		configPath:  configPath,
		deps: appjob.Deps{
			Convs:        convs,
			Providers:    providers,
			Environments: environments,
			Config:       ccfg,
			// A new thread starts on this effort: spawns and automations have
			// no picker to send one, so the setting is their only source.
			ThreadsEffort: cfg.Models.Threads.Effort,
			Terminals:     terminals,
			SummaryTool:   summaryTool,
		},
		workers:      map[string]*worker.Worker{},
		turning:      map[string]int{},
		consumed:     map[string]time.Time{},
		wakeQueued:   map[string]bool{},
		machineTurns: map[string]int{},
		stopGrace:    10 * time.Second,
	}
	// The turn claim is the app's own: a job reports the room busy for exactly
	// as long as its turn runs, which is what a pick has to respect.
	app.deps.Turns = app
	app.deps.Log = slog.Default()
	// A line the room already wrote reaches a following turn through the same
	// accept path: the job carries the record, so no second line is written.
	app.deps.FollowUp = func(convID string, rec entity.Message) error {
		_, err := app.Enqueue(context.Background(), convID, "", "", rec.Content(), rec.Attachments(),
			appjob.SendOptions{Origin: valueobject.TurnOrigin{Kind: enum.TurnOriginCompletion}, AcceptedRecord: &rec})
		return err
	}
	app.deps.Completions = app
	for _, opt := range opts {
		opt(app)
	}
	return app
}

// WithProjects registers the project store: rooms in a project work in its
// folders, and rooms can move between projects.
func WithProjects(projects drepo.Project) Option {
	return func(app *ConversationApp) { app.deps.Projects = projects }
}

// OnDeleted adds fn to run after a room is deleted. Use cases built
// after the broker (chat channels) register here to drop what they keep
// about the room.
func (app *ConversationApp) OnDeleted(fn func(convID string)) {
	app.mu.Lock()
	prev := app.deps.OnDeleted
	app.deps.OnDeleted = func(convID string) {
		if prev != nil {
			prev(convID)
		}
		fn(convID)
	}
	app.mu.Unlock()
}

// OnCompacted adds fn to run after a room's history is compacted, with the
// messages the compaction sealed. fn must not block: it runs inside the
// room's turn.
func (app *ConversationApp) OnCompacted(fn func(convID string, sealed []entity.Message)) {
	app.mu.Lock()
	prev := app.deps.OnCompacted
	app.deps.OnCompacted = func(convID string, sealed []entity.Message) {
		if prev != nil {
			prev(convID, sealed)
		}
		fn(convID, sealed)
	}
	app.mu.Unlock()
}

// OnChanged registers fn to hear of a room changing outside a turn.
func (app *ConversationApp) OnChanged(fn func(convID string)) {
	app.mu.Lock()
	app.changed = append(app.changed, fn)
	app.mu.Unlock()
}

// RecordDelivery records a reply's trip to a chat on its message — the
// files that went with it and the chat's receipt — and tells open views.
func (app *ConversationApp) RecordDelivery(ctx context.Context, convID, msgID string, files []valueobject.Attachment, receipt valueobject.Receipt) error {
	if err := app.depsSnapshot().Convs.RecordDelivery(ctx, convID, msgID, files, receipt); err != nil {
		return err
	}
	app.notifyChanged(convID)
	return nil
}

func (app *ConversationApp) notifyChanged(convID string) {
	app.mu.Lock()
	hooks := slices.Clone(app.changed)
	app.mu.Unlock()
	for _, fn := range hooks {
		fn(convID)
	}
}

// UseApprovalRules sets where rooms' "always allow" answers are kept.
func (app *ConversationApp) UseApprovalRules(rules drepo.ApprovalRule) {
	app.mu.Lock()
	app.deps.ApprovalRules = rules
	app.mu.Unlock()
}

// ApprovalRules lists a room's "always allow" rules.
func (app *ConversationApp) ApprovalRules(ctx context.Context, convID string) ([]aggregate.ApprovalRule, error) {
	deps := app.depsSnapshot()
	if deps.ApprovalRules == nil {
		return nil, nil
	}
	return deps.ApprovalRules.List(ctx, convID)
}

// DeleteApprovalRule forgets one, so the agent asks again.
func (app *ConversationApp) DeleteApprovalRule(ctx context.Context, convID, id string) error {
	deps := app.depsSnapshot()
	if deps.ApprovalRules == nil {
		return apperror.NewNotFoundError("approval rule "+id, nil)
	}
	return deps.ApprovalRules.Delete(ctx, convID, id)
}

// UseMCPServers sets the tool servers an agent gets in each room.
func (app *ConversationApp) UseMCPServers(fn func(convID string) []dservice.MCPServer) {
	app.mu.Lock()
	app.deps.MCPServers = fn
	app.mu.Unlock()
}

// Option configures optional collaborators at construction.
type Option func(*ConversationApp)

// WithAgents registers the agent runtime: rooms bound to a configured
// agent run their turns through it, and their permission requests wait in
// a broker the owner answers through Approve.
func WithAgents(agents drepo.Agent, catalog drepo.AgentCatalogStore, runtime dservice.AgentRuntime) Option {
	return func(app *ConversationApp) {
		app.deps.Agents = agents
		app.deps.AgentCatalog = catalog
		app.deps.AgentRuntime = runtime
		app.deps.Approvals = appjob.NewApprovals()
		app.deps.Elicitations = appjob.NewElicitations(appjob.OwnerAnswerTimeout)
	}
}

// depsSnapshot returns a shallow copy of deps under app.mu so entry points
// do not read app.deps concurrently with the reload inside post.
func (app *ConversationApp) depsSnapshot() appjob.Deps {
	app.mu.Lock()
	defer app.mu.Unlock()
	return app.deps
}

// post enqueues a job on the room's worker, spawning one on demand.
// Preempt jobs (edit, delete) cut the line and interrupt whatever runs;
// ordinary jobs append FIFO. Posting stays under app.mu through Submit so
// Shutdown has a clear boundary between accepted and rejected work. A
// submit racing a worker's teardown gets ErrClosed — respawn and retry.
// post enqueues a job on the room's worker and answers the refusal, if any:
// nil means the worker accepted it. A job accepted here may still fail later —
// post only reports admission, never the turn's outcome.
func (app *ConversationApp) post(convID string, j queuedJob) error {
	for {
		app.mu.Lock()
		// Live config rides each post — a settings change reaches the
		// next submitted job without restarting the broker; a failed
		// reload keeps the last good values.
		if app.configPath != "" {
			var fresh config.Config
			if err := fresh.Load(app.configStore, app.configPath); err == nil {
				ccfg := fresh.Conversation
				ccfg.Normalize()
				app.cfg = ccfg
				app.deps.Config = ccfg
				app.deps.ThreadsEffort = fresh.Models.Threads.Effort
			}
		}
		deps := app.deps
		cfg := app.cfg
		switch x := j.(type) {
		case *appjob.Send:
			x.Deps = deps
		case *appjob.Edit:
			x.Deps = deps
		case *appjob.Resume:
			x.Deps = deps
		case *appjob.Delete:
			x.Deps = deps
		case *appjob.Compact:
			x.Deps = deps
		case *appjob.Clear:
			x.Deps = deps
		case *appjob.Fork:
			x.Deps = deps
		case *appjob.Annotate:
			x.Deps = deps
		case *appjob.Wake:
			x.Deps = deps
		}
		if app.shuttingDown {
			app.mu.Unlock()
			refusal := apperror.New(503, "conversation application is shutting down", nil)
			j.Reply(refusal)
			return refusal
		}
		w := app.workers[convID]
		if w == nil || w.Dead() {
			w = worker.New(worker.Options{
				QueueCap: cfg.MailboxCap,
				OnIdle: func(idle *worker.Worker) {
					// The queue drained: the room's surface goes back to
					// zero waiting, read from the queue itself.
					app.notifyQueue(convID)
					app.evict(convID, idle)
					// A record that arrived while the room worked had no turn
					// to read it; the room going idle is what retries. The
					// record waited, not the room.
					app.wakeIfPending(convID)
				},
				OnTerminate: func(term *worker.Worker, drained []worker.Job) {
					// A self-terminated worker (a delete job retired it):
					// queued jobs die with the room — 410 Gone — and the
					// map entry goes, so the next post spawns a fresh
					// worker.
					gone := apperror.New(410, "conversation deleted", fmt.Errorf("room %s removed", convID))
					for _, j := range drained {
						if aj, ok := j.(queuedJob); ok {
							aj.Reply(gone)
						}
					}
					app.mu.Lock()
					if app.workers[convID] == term {
						delete(app.workers, convID)
					}
					app.mu.Unlock()
				},
			})
			app.workerWG.Add(1)
			go func(w *worker.Worker) {
				<-w.Done()
				app.workerWG.Done()
			}(w)
			app.workers[convID] = w
		}

		// An event handler runs on the worker's own goroutine — a
		// synchronous same-room post from inside it would queue behind
		// the very job hosting the handler and deadlock until the
		// caller's ctx dies. Refuse instead; the handler must defer the
		// intent to its own goroutine.
		if w.OnWorkerGoroutine() {
			app.mu.Unlock()
			refusal := apperror.New(409, "cannot post to a conversation from inside its own turn", fmt.Errorf("reentrant same-room post on %s", convID))
			j.Reply(refusal)
			return refusal
		}

		j.Bind(convID, w)
		var err error
		if j.IsPreempt() {
			err = w.Preempt(j)
		} else {
			err = w.Submit(j)
		}
		app.mu.Unlock()

		switch {
		case errors.Is(err, worker.ErrClosed):
			continue
		case errors.Is(err, worker.ErrFull):
			refusal := apperror.New(429, "conversation queue full", fmt.Errorf("mailbox capacity %d", cfg.MailboxCap))
			j.Reply(refusal)
			return refusal
		}
		return nil
	}
}

// evict retires an idle worker — called by the worker's OnIdle hook and
// from paths that discover an idle worker (e.g. Cancel finding nothing
// running). Anything submitted in the race window lands in Terminate's
// drain and is reposted onto the successor worker.
func (app *ConversationApp) evict(convID string, w *worker.Worker) {
	app.mu.Lock()
	if app.workers[convID] != w || !w.Idle() {
		app.mu.Unlock()
		return
	}
	drained := w.Terminate()
	delete(app.workers, convID)
	app.mu.Unlock()
	for _, j := range drained {
		if aj, ok := j.(queuedJob); ok {
			app.post(convID, aj) // IsPreempt rides on the job, not the call site
		}
	}
}
