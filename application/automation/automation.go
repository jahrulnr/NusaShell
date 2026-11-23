// Package appautomation runs prompts on a schedule. Each firing is one run:
// a turn in a thread — run through the same turn path the web uses, so the
// web sees it live — recorded in the automation's history, with the final
// reply optionally sent to a chat.
package appautomation

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Runner runs turns the way the web does. The WebSocket hub implements it.
type Runner interface {
	// Run opens a send turn and blocks until it ends.
	Run(ctx context.Context, convID, providerID, modelID, content string, attachments []valueobject.Attachment, opts appjob.SendOptions, observe event.TurnEventHandler) (string, appconversation.SendResult, error)
}

// Rooms is what runs read and change on threads.
type Rooms interface {
	Info(ctx context.Context, id string) (drepo.ConversationInfo, error)
	Annotate(ctx context.Context, convID string, a appconversation.Annotation) error
}

// Chats sends to chat channels. *appchannel.App satisfies it.
type Chats interface {
	Send(ctx context.Context, chat valueobject.ChatRef, text string) error
	SendFinal(ctx context.Context, convID string, chat valueobject.ChatRef) error
}

// Alerts hears how runs end, for desktop notifications.
type Alerts interface {
	AutomationRan(name string, status enum.RunStatus, note string)
}

// historyLimit is how many runs an automation's history shows.
const historyLimit = 50

// keepRuns is how many finished runs an automation keeps; older history
// is pruned when a run ends.
const keepRuns = 100

// tick is how often the scheduler looks for due automations.
const tick = 5 * time.Second

// App is the automation use case and its scheduler.
type App struct {
	repo     drepo.Automation
	projects drepo.Project
	runner   Runner
	rooms    Rooms
	chats    Chats
	alerts   Alerts
	zone     string
	log      *slog.Logger
	now      func() time.Time

	mu      sync.Mutex
	cancel  context.CancelFunc
	ctx     context.Context
	stopped bool
	wg      sync.WaitGroup
}

// New wires the use case. zone is the server's time zone, the default for
// new schedules. chats and alerts may be nil.
func New(repo drepo.Automation, projects drepo.Project, runner Runner, rooms Rooms, chats Chats, alerts Alerts, zone string, log *slog.Logger) *App {
	if log == nil {
		log = slog.Default()
	}
	return &App{repo: repo, projects: projects, runner: runner, rooms: rooms, chats: chats, alerts: alerts, zone: zone,
		log: log.With("component", "automations"), now: time.Now, ctx: context.Background()}
}

// Zone is the server's time zone.
func (a *App) Zone() string { return a.zone }

// Chat names a result chat.
type Chat struct {
	Channel string `json:"channel"`
	ChatID  string `json:"chatId"`
	Thread  string `json:"thread,omitempty"`
}

// Input is a full automation document from the owner.
type Input struct {
	Name       string
	Prompt     string
	AgentID    string
	ProviderID string
	ModelID    string
	Schedule   string // "once" or "cron"
	RunAt      time.Time
	Cron       string
	Timezone   string
	Thread     string // "new" or "same"; empty is "new"
	ProjectID  string
	Notify     *Chat
	ExpiresAt  time.Time
	// Enabled is nil when the document says nothing: a new automation is
	// enabled, an edited one keeps its current state.
	Enabled *bool
}

// View is an automation for the web.
type View struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Prompt         string     `json:"prompt"`
	AgentID        string     `json:"agentId,omitempty"`
	ProviderID     string     `json:"providerId,omitempty"`
	ModelID        string     `json:"modelId,omitempty"`
	Schedule       string     `json:"schedule"`
	RunAt          *time.Time `json:"runAt,omitempty"`
	Cron           string     `json:"cron,omitempty"`
	Timezone       string     `json:"timezone"`
	Thread         string     `json:"thread"`
	ConversationID string     `json:"conversationId,omitempty"`
	ProjectID      string     `json:"projectId,omitempty"`
	Notify         *Chat      `json:"notify,omitempty"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
	Enabled        bool       `json:"enabled"`
	Expired        bool       `json:"expired"`
	NextRunAt      *time.Time `json:"nextRunAt,omitempty"`
	LastRun        *RunView   `json:"lastRun,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

// RunView is one run for the web.
type RunView struct {
	ID             string     `json:"id"`
	ScheduledAt    time.Time  `json:"scheduledAt"`
	StartedAt      time.Time  `json:"startedAt"`
	EndedAt        *time.Time `json:"endedAt,omitempty"`
	ConversationID string     `json:"conversationId,omitempty"`
	Status         string     `json:"status"`
	Note           string     `json:"note,omitempty"`
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (a *App) view(au aggregate.Automation, last *entity.AutomationRun) View {
	s := au.Schedule()
	v := View{ID: au.ID(), Name: au.Name(), Prompt: au.Prompt(), AgentID: au.AgentID(), ProviderID: au.ProviderID(), ModelID: au.ModelID(),
		Schedule: string(s.Kind()), RunAt: timePtr(s.At()), Cron: s.Cron(), Timezone: s.Zone(), Thread: au.Thread().String(),
		ConversationID: au.ConversationID(), ProjectID: au.ProjectID(), ExpiresAt: timePtr(au.ExpiresAt()), Enabled: au.Enabled(),
		Expired: au.Expired(a.now()), NextRunAt: timePtr(au.NextRunAt()), CreatedAt: au.CreatedAt(), UpdatedAt: au.UpdatedAt()}
	if n := au.Notify(); n != nil {
		v.Notify = &Chat{Channel: n.Channel(), ChatID: n.ChatID(), Thread: n.Thread()}
	}
	if last != nil {
		rv := runView(*last)
		v.LastRun = &rv
	}
	return v
}

func runView(r entity.AutomationRun) RunView {
	return RunView{ID: r.ID(), ScheduledAt: r.ScheduledAt(), StartedAt: r.StartedAt(), EndedAt: timePtr(r.EndedAt()),
		ConversationID: r.ConversationID(), Status: r.Status().String(), Note: r.Note()}
}

func invalid(err error) error { return apperror.New(400, err.Error(), err) }

// apply writes in onto au, validating each part.
func (a *App) apply(ctx context.Context, au *aggregate.Automation, in Input, now time.Time) error {
	if err := au.Describe(in.Name, in.Prompt); err != nil {
		return invalid(err)
	}
	if err := au.SetRuntime(in.AgentID, in.ProviderID, in.ModelID); err != nil {
		return invalid(err)
	}
	zone := strings.TrimSpace(in.Timezone)
	if zone == "" {
		zone = a.zone
	}
	var (
		s   valueobject.Schedule
		err error
	)
	switch valueobject.ScheduleKind(in.Schedule) {
	case valueobject.ScheduleOnce:
		s, err = valueobject.NewOnceSchedule(in.RunAt, zone)
	case valueobject.ScheduleCron:
		s, err = valueobject.NewCronSchedule(in.Cron, zone)
	default:
		return apperror.New(400, "schedule must be once or cron", nil)
	}
	if err != nil {
		return invalid(err)
	}
	if err := au.SetSchedule(s, now); err != nil {
		return invalid(err)
	}
	thread, ok := enum.ParseAutomationThread(in.Thread)
	if in.Thread == "" {
		thread, ok = enum.AutomationThreadNew, true
	}
	if !ok {
		return apperror.New(400, "thread must be new or same", nil)
	}
	au.SetThread(thread)
	if id := strings.TrimSpace(in.ProjectID); id != "" && a.projects != nil {
		if _, err := a.projects.Get(ctx, id); err != nil {
			if apperror.As(err).StatusCode() == apperror.StatusNotFound {
				return apperror.New(400, "unknown project "+id, err)
			}
			return err
		}
	}
	au.SetProject(in.ProjectID)
	if in.Notify != nil {
		chat, err := valueobject.NewChatRef(in.Notify.Channel, in.Notify.ChatID, in.Notify.Thread)
		if err != nil {
			return invalid(err)
		}
		au.SetNotify(&chat)
	} else {
		au.SetNotify(nil)
	}
	if err := au.SetExpiry(in.ExpiresAt, now); err != nil {
		return invalid(err)
	}
	enabled := au.Enabled()
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	au.SetEnabled(enabled, now)
	return nil
}

// List returns every automation with its last run.
func (a *App) List(ctx context.Context) ([]View, error) {
	list, err := a.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(list))
	for _, au := range list {
		runs, err := a.repo.Runs(ctx, au.ID(), 1)
		if err != nil {
			return nil, err
		}
		var last *entity.AutomationRun
		if len(runs) > 0 {
			last = &runs[0]
		}
		out = append(out, a.view(au, last))
	}
	return out, nil
}

// Get returns one automation.
func (a *App) Get(ctx context.Context, id string) (View, error) {
	au, err := a.repo.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	return a.view(*au, nil), nil
}

// Runs returns an automation's recent runs, newest first.
func (a *App) Runs(ctx context.Context, id string) ([]RunView, error) {
	if _, err := a.repo.Get(ctx, id); err != nil {
		return nil, err
	}
	runs, err := a.repo.Runs(ctx, id, historyLimit)
	if err != nil {
		return nil, err
	}
	out := make([]RunView, 0, len(runs))
	for _, r := range runs {
		out = append(out, runView(r))
	}
	return out, nil
}

// Create saves a new automation.
func (a *App) Create(ctx context.Context, in Input) (View, error) {
	now := a.now()
	au, err := aggregate.NewAutomation("", in.Name, in.Prompt, now)
	if err != nil {
		return View{}, invalid(err)
	}
	if err := a.apply(ctx, au, in, now); err != nil {
		return View{}, err
	}
	if err := a.repo.Create(ctx, au); err != nil {
		return View{}, err
	}
	return a.view(*au, nil), nil
}

// Update replaces an automation. readAt is the updatedAt the edit started
// from; an automation the owner saved since answers 409. A firing does
// not change updatedAt.
func (a *App) Update(ctx context.Context, id string, in Input, readAt time.Time) (View, error) {
	au, err := a.repo.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	if !au.UpdatedAt().Equal(readAt) {
		return View{}, apperror.New(409, "the automation was changed elsewhere — reload it and try again", aggregate.ErrAutomationChanged)
	}
	now := a.now()
	if err := a.apply(ctx, au, in, now); err != nil {
		return View{}, err
	}
	au.Touch(now)
	if err := a.repo.Update(ctx, au, readAt); err != nil {
		return View{}, err
	}
	return a.view(*au, nil), nil
}

// SetEnabled pauses or resumes an automation.
func (a *App) SetEnabled(ctx context.Context, id string, enabled bool) (View, error) {
	au, err := a.repo.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	for retry := true; ; retry = false {
		readAt := au.UpdatedAt()
		au.SetEnabled(enabled, a.now())
		au.Touch(a.now())
		if err := a.repo.Update(ctx, au, readAt); err == nil {
			return a.view(*au, nil), nil
		} else if !retry || !errors.Is(err, aggregate.ErrAutomationChanged) {
			return View{}, err
		}
		// A claim or keep landed between the read and the write; take the
		// fresh row and retry the same state change once.
		if au, err = a.repo.Get(ctx, id); err != nil {
			return View{}, err
		}
	}
}

// Delete removes an automation and its history. Its threads stay.
func (a *App) Delete(ctx context.Context, id string) error { return a.repo.Delete(ctx, id) }

// RunNow starts one run at once, leaving the schedule as it is. A run
// still going answers 409 — one automation has at most one.
func (a *App) RunNow(ctx context.Context, id string) (RunView, error) {
	a.mu.Lock()
	stopped := a.stopped
	a.mu.Unlock()
	if stopped {
		return RunView{}, apperror.New(503, "the server is shutting down", nil)
	}
	au, err := a.repo.Get(ctx, id)
	if err != nil {
		return RunView{}, err
	}
	now := a.now()
	run, err := entity.NewAutomationRun(au.ID(), now, now)
	if err != nil {
		return RunView{}, err
	}
	ok, err := a.repo.StartRun(ctx, run)
	if err != nil {
		return RunView{}, err
	}
	if !ok {
		return RunView{}, apperror.New(409, "a run of this automation is still going", nil)
	}
	view := runView(*run) // before the run's goroutine owns it
	a.start(*au, run)
	return view, nil
}

// TestNotify sends a short message to the automation's result chat, so a
// wrong chat shows up now rather than at the next run.
func (a *App) TestNotify(ctx context.Context, id string) error {
	au, err := a.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	chat := au.Notify()
	if chat == nil {
		return apperror.New(400, "this automation sends its result to no chat", nil)
	}
	if a.chats == nil {
		return apperror.New(503, "chat channels are not available", nil)
	}
	return a.chats.Send(ctx, *chat, "Test from automation “"+au.Name()+"”: results will arrive in this chat.")
}

// Start marks runs left over from a previous process as interrupted and
// starts the scheduler.
func (a *App) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.ctx, a.cancel = ctx, cancel
	a.mu.Unlock()
	if err := a.repo.InterruptRuns(ctx, a.now()); err != nil {
		a.log.Warn("could not close interrupted runs", "error", err)
	}
	a.wg.Go(func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			a.Tick(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	})
}

// Stop stops the scheduler, cancels running runs, and waits for them to
// be recorded. After it, no run starts — claimed or manual — and RunNow
// answers 503.
func (a *App) Stop() {
	a.mu.Lock()
	a.stopped = true
	cancel := a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.wg.Wait()
}

// Tick starts a run of every due automation.
func (a *App) Tick(ctx context.Context) {
	now := a.now()
	due, err := a.repo.Due(ctx, now)
	if err != nil {
		a.log.Warn("could not list due automations", "error", err)
		return
	}
	for _, au := range due {
		if ctx.Err() != nil {
			return
		}
		if !au.Due(now) {
			continue // expired
		}
		scheduled := au.Claim(now)
		run, err := entity.NewAutomationRun(au.ID(), scheduled, now)
		if err != nil {
			a.log.Warn("could not start a run", "automation", au.ID(), "error", err)
			continue
		}
		ok, err := a.repo.Claim(ctx, &au, run)
		if err != nil {
			a.log.Warn("could not claim an automation", "automation", au.ID(), "error", err)
			continue
		}
		if ok {
			a.start(au, run)
		}
	}
}

func (a *App) start(au aggregate.Automation, run *entity.AutomationRun) {
	a.mu.Lock()
	if a.stopped {
		a.mu.Unlock()
		return // shutting down: no new run — the leftover row is interrupted at next start
	}
	ctx := a.ctx
	a.mu.Unlock()
	a.wg.Go(func() { a.launch(ctx, au, run) })
}

// launch runs one turn for run and records how it ended.
func (a *App) launch(ctx context.Context, au aggregate.Automation, run *entity.AutomationRun) {
	finish := func(status enum.RunStatus, note string) {
		// An automation deleted while its run was going leaves nothing to
		// record or alert — the delete already took its history.
		if _, err := a.repo.Get(context.Background(), au.ID()); apperror.As(err).StatusCode() == apperror.StatusNotFound {
			return
		}
		run.Finish(status, note, a.now())
		if err := a.repo.SaveRun(context.Background(), run); err != nil {
			a.log.Warn("could not record a run", "automation", au.ID(), "error", err)
		}
		if err := a.repo.PruneRuns(context.Background(), au.ID(), keepRuns); err != nil {
			a.log.Warn("could not prune run history", "automation", au.ID(), "error", err)
		}
		if a.alerts != nil {
			a.alerts.AutomationRan(au.Name(), run.Status(), run.Note())
		}
	}
	if au.Expired(a.now()) {
		finish(enum.RunFailed, "the automation expired before the run started")
		return
	}
	convID := ""
	if au.Thread() == enum.AutomationThreadSame && au.ConversationID() != "" {
		_, err := a.rooms.Info(ctx, au.ConversationID())
		switch {
		case err == nil:
			convID = au.ConversationID()
		case apperror.As(err).StatusCode() == apperror.StatusNotFound:
			// The kept thread is gone — the run opens a new one.
		default:
			finish(enum.RunFailed, apperror.As(err).Error())
			return
		}
	}
	fresh := convID == ""
	if fresh {
		conv, err := aggregate.NewConversation("")
		if err != nil {
			finish(enum.RunFailed, err.Error())
			return
		}
		convID = conv.ID()
		if au.Thread() == enum.AutomationThreadSame {
			// Keep the thread for the next runs. An edit racing this run
			// wins; the next run then opens its own thread.
			au.KeepConversation(convID)
			ok, err := a.repo.KeepConversation(context.Background(), au.ID(), convID, au.Version())
			if err != nil || !ok {
				a.log.Warn("could not keep an automation's thread", "automation", au.ID(), "error", err)
			}
		}
	}
	run.Bind(convID)
	if err := a.repo.SaveRun(context.Background(), run); err != nil {
		a.log.Warn("could not record a run", "automation", au.ID(), "error", err)
	}
	opts := appjob.SendOptions{Agent: au.AgentID(), Project: au.ProjectID()}
	_, _, err := a.runner.Run(ctx, convID, au.ProviderID(), au.ModelID(), au.Prompt(), nil, opts, nil)
	if err != nil {
		// If the room is gone afterwards — deleted while the turn ran —
		// history must not link to a thread that does not exist.
		if _, ierr := a.rooms.Info(context.Background(), convID); apperror.As(ierr).StatusCode() == apperror.StatusNotFound {
			run.Bind("")
			fresh = false
		}
	}
	if fresh {
		title := "Automation · " + au.Name()
		_ = a.rooms.Annotate(context.Background(), convID, appconversation.Annotation{Title: &title})
	}
	switch {
	case err == nil:
	case ctx.Err() != nil:
		finish(enum.RunInterrupted, "the server stopped during the run")
		return
	case apperror.As(err).StatusCode() == 408:
		finish(enum.RunCancelled, "stopped")
		return
	default:
		finish(enum.RunFailed, apperror.As(err).Error())
		return
	}
	if chat := au.Notify(); chat != nil {
		if a.chats == nil {
			run.AddNote("the reply was not sent: chat channels are not available")
		} else if err := a.chats.SendFinal(context.Background(), convID, *chat); err != nil {
			run.AddNote("the reply did not reach the chat: " + apperror.As(err).Error())
		}
	}
	finish(enum.RunCompleted, run.Note())
}
