package appconversation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoagent "github.com/jahrulnr/nusashell/infrastructure/repository/agent"
	repoconversation "github.com/jahrulnr/nusashell/infrastructure/repository/conversation"
	repoprovider "github.com/jahrulnr/nusashell/infrastructure/repository/provider"
	reposecret "github.com/jahrulnr/nusashell/infrastructure/repository/secret"
	coretext "github.com/jahrulnr/nusashell/infrastructure/service/core/text"
	textcompletion "github.com/jahrulnr/nusashell/infrastructure/service/core/text/completion"
)

// scriptedAgent is an AgentRuntime that runs one read tool behind an
// approval, then answers.
type scriptedAgent struct {
	mu        sync.Mutex
	prompts   []dservice.AgentPrompt
	sessions  []string // session handle each prompt arrived with
	released  []string
	discarded []string
	answers   []valueobject.ElicitationAnswer
	// steerable makes a "wait for steer" prompt hold until Steer delivers.
	steerable bool
	// steers is every text the agent took mid-turn, in order.
	steers  []string
	steered chan string
	// report is the context account the agent gives before it finishes.
	report *valueobject.AgentContext
	// title is the name the agent suggests for the room.
	title string
	// rotate opens a new session whenever the room holds none.
	rotate bool
	// retired lists "agent/session" for every session the app asked to remove.
	retired []string
	// yielded lets a "wait for yield" prompt end: it closes when the app
	// asks the agent to stop at a safe point. yields counts the asks.
	yielded chan struct{}
	yields  int
	// beforePrompt delays the moment the scripted agent takes the prompt,
	// holding the send open while session setup has already run.
	beforePrompt func(context.Context) error
	// refuseValue is the one option value the agent turns down.
	refuseValue string
	// options replaces the offered list when set — a session that no longer
	// offers an option a stored catalog still remembers.
	options []dservice.AgentOption
	// emptyLive makes the reading answer what a live session that has offered
	// nothing yet answers: no options, and the transient OptionsEmpty state.
	emptyLive bool
}

func (a *scriptedAgent) Prompt(ctx context.Context, p dservice.AgentPrompt, onEvent event.TurnEventHandler, approve dservice.Approver) error {
	a.mu.Lock()
	a.prompts = append(a.prompts, p)
	a.sessions = append(a.sessions, p.Conversation.AgentSession())
	a.mu.Unlock()
	// A real agent opens a new session when the room has none; rotate makes
	// the scripted one do the same, where the default reuses one id.
	session := "sess-" + p.Conversation.ID()
	if a.rotate && p.Conversation.AgentSession() == "" {
		session += "-" + strconv.Itoa(len(a.prompts))
	} else if a.rotate {
		session = p.Conversation.AgentSession()
	}
	p.Conversation.SetAgentSession(session)
	if a.beforePrompt != nil {
		if err := a.beforePrompt(ctx); err != nil {
			return err
		}
	}
	if p.OnStarted != nil {
		p.OnStarted()
	}
	call, err := entity.NewToolCall("call-1", "Read files", `{"path":"."}`, enum.TooltypeDelegated)
	if err != nil {
		return err
	}
	onEvent(event.NewToolCallEvent(call))
	if strings.Contains(p.Input.Content(), "wait for steer") && a.steerable {
		select {
		case text := <-a.steered:
			onEvent(event.NewSteerEvent(text))
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// An agent that cannot take input mid-turn is asked to yield; its turn
	// then ends as an interrupted one, and the input starts the next prompt.
	if strings.Contains(p.Input.Content(), "wait for yield") && a.yielded != nil {
		select {
		case <-a.yielded:
		case <-ctx.Done():
			return ctx.Err()
		}
		stopped, _ := entity.NewMessage(enum.RoleAssistant, "stopped")
		stopped.SetFinishReason(enum.FinishReasonInterrupted)
		p.Conversation.Append(stopped)
		return nil
	}
	// A prompt asking for a form asks the owner first through Elicit.
	if strings.Contains(p.Input.Content(), "form") && p.Elicit != nil {
		e, err := valueobject.NewElicitation("eli_1", "form", "Pick one", json.RawMessage(`{"type":"object","properties":{"env":{"type":"string","enum":["dev","prod"]}},"required":["env"]}`), "")
		if err != nil {
			return err
		}
		ans, err := p.Elicit(ctx, e)
		if err != nil {
			return err
		}
		a.mu.Lock()
		a.answers = append(a.answers, ans)
		a.mu.Unlock()
	}
	req, err := valueobject.NewApprovalRequest("apr_1", "call-1", "Read files", `{"path":"."}`, enum.ToolActionRead, []valueobject.ApprovalOption{
		{ID: "yes", Name: "Allow", Choice: enum.ApprovalAllowOnce},
		{ID: "always", Name: "Always allow", Choice: enum.ApprovalAllowAlways},
		{ID: "no", Name: "Reject", Choice: enum.ApprovalRejectOnce},
	})
	if err != nil {
		return err
	}
	optionID, err := approve(ctx, req)
	msg, _ := entity.NewMessage(enum.RoleAssistant, "")
	_ = msg.AddToolCall(call)
	if err != nil {
		_ = msg.SetToolOutput("call-1", "interrupted")
		msg.SetFinishReason(enum.FinishReasonInterrupted)
		p.Conversation.Append(msg)
		return apperror.New(408, "turn canceled", err)
	}
	if opt, _ := req.Option(optionID); opt.Choice == enum.ApprovalAllowOnce || opt.Choice == enum.ApprovalAllowAlways {
		_ = msg.SetToolOutput("call-1", "README.md")
		msg.SetContent("found README.md")
	} else {
		_ = msg.SetToolOutput("call-1", "denied")
		msg.SetContent("not allowed")
	}
	msg.SetFinishReason(enum.FinishReasonStop)
	onEvent(event.NewDeltaEvent(msg.Content()))
	if a.report != nil {
		onEvent(event.NewContextEvent(*a.report))
	}
	if a.title != "" {
		onEvent(event.NewTitleEvent(a.title))
	}
	usage, _ := valueobject.NewUsage(7, 0, 0, 3)
	onEvent(event.NewFinishedEvent(enum.FinishReasonStop, usage))
	p.Conversation.Append(msg)
	return nil
}

func (a *scriptedAgent) Models(context.Context, *aggregate.Agent) ([]dservice.AgentModel, error) {
	return nil, nil
}

func (a *scriptedAgent) Release(_ context.Context, convID string) {
	a.mu.Lock()
	a.released = append(a.released, convID)
	a.mu.Unlock()
}
func (a *scriptedAgent) Reload(string) {}
func (a *scriptedAgent) Yield(context.Context, string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.yields++
	if a.yielded != nil {
		select {
		case <-a.yielded:
		default:
			close(a.yielded)
		}
	}
	return a.yielded != nil
}
func (a *scriptedAgent) Discard(_ context.Context, agent *aggregate.Agent, convID, sessionID string) {
	a.mu.Lock()
	a.discarded = append(a.discarded, convID)
	a.retired = append(a.retired, agent.ID()+"/"+sessionID)
	a.mu.Unlock()
}

// retiredSessions waits for the background removals to land.
func (a *scriptedAgent) retiredSessions(t *testing.T, want int) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		a.mu.Lock()
		got := slices.Clone(a.retired)
		a.mu.Unlock()
		if len(got) >= want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func (a *scriptedAgent) Warm(context.Context, *aggregate.Agent) error { return nil }
func (a *scriptedAgent) ReapIdle(time.Duration)                       {}
func (a *scriptedAgent) Options(context.Context, *aggregate.Agent, string) ([]dservice.AgentOption, dservice.OptionsState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.emptyLive {
		return nil, dservice.OptionsEmpty, nil
	}
	if a.options != nil {
		return a.options, dservice.OptionsReady, nil
	}
	// The catalog a real agent reports. The picker and the store now validate
	// against it, so the scripted agent has to offer what a test picks.
	return []dservice.AgentOption{
		{ID: "mode", Name: "Mode", Category: "mode", Type: "select", Current: "ask",
			Choices: []dservice.OptionChoice{{Value: "ask", Name: "Ask"}, {Value: "build", Name: "Build"}}},
		{ID: "strict", Name: "Strict", Category: "behavior", Type: "boolean", Current: "false"},
	}, dservice.OptionsReady, nil
}
func (a *scriptedAgent) SetOption(_ context.Context, _, _, value string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	// A live agent refuses a value it does not have — a mode it never
	// declared, say — and the refusal must not be swallowed.
	if a.refuseValue != "" && value == a.refuseValue {
		return apperror.New(502, "agent refused the mode change", nil)
	}
	return nil
}
func (a *scriptedAgent) Commands(string) []dservice.AgentCommand { return nil }
func (a *scriptedAgent) Status(string) dservice.AgentStatus      { return dservice.AgentStatus{} }
func (a *scriptedAgent) Steer(_ context.Context, _ string, input valueobject.UserInput, delivered func()) bool {
	if !a.steerable {
		return false
	}
	a.mu.Lock()
	a.steers = append(a.steers, input.Text)
	a.mu.Unlock()
	delivered()
	a.steered <- input.Text
	return true
}
func (a *scriptedAgent) Authenticate(context.Context, *aggregate.Agent, string) error { return nil }
func (a *scriptedAgent) SignInCommand(context.Context, *aggregate.Agent, string) (dservice.ShellCommand, error) {
	return dservice.ShellCommand{}, nil
}
func (a *scriptedAgent) Logout(context.Context, *aggregate.Agent) error { return nil }
func (a *scriptedAgent) Shutdown(context.Context) error                 { return nil }

// agentFixture wires a provider stub (capturing request bodies) and the
// scripted agent into one app.
func agentFixture(t *testing.T, policy enum.ApprovalPolicy, opts ...Option) (*ConversationApp, *scriptedAgent, *[]string) {
	t.Helper()
	var bodies []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		okHandler(w, r)
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
	agents := repoagent.New(t.TempDir(), secrets)
	a, _ := aggregate.NewAgent("fake", "Fake", "fake-acp", nil, policy)
	if err := agents.Save(t.Context(), &a); err != nil {
		t.Fatal(err)
	}
	rt := &scriptedAgent{}
	app := New(
		repoconversation.NewConversationRepository(t.TempDir()),
		providers,
		map[enum.Environment]appjob.EnvironmentDriver{enum.EnvironmentLocal: appjob.EnvironmentBundle{
			DefaultRoot: t.TempDir(),
			Services:    map[enum.Style]dservice.TextModelService{enum.StyleCompletion: textcompletion.New(coretext.Options{})},
		}},
		config.NewConfig(), nil, "", nil, nil,
		append([]Option{WithAgents(agents, agents, rt)}, opts...)...,
	)
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	return app, rt, &bodies
}

func TestAgentTurnWaitsForOwnerApproval(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAsk)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	asked := make(chan valueobject.ApprovalRequest, 1)
	var kinds []enum.TurnEventKind
	onEvent := func(ev event.TurnEvent) {
		kinds = append(kinds, ev.Kind())
		if ev.Kind() == enum.TurnEventApproval {
			asked <- ev.Approval()
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err := app.Send(ctx, "c_agent1", "", "fake-smart", "look around", nil, appjob.SendOptions{Agent: "fake"}, onEvent)
		done <- err
	}()
	req := <-asked
	if pending := app.PendingApprovals("c_agent1"); len(pending) != 1 || pending[0].ID() != req.ID() {
		t.Fatalf("pending = %v", pending)
	}
	if err := app.Approve("c_agent1", req.ID(), "maybe"); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("unoffered option err = %v, want 400", err)
	}
	if err := app.Approve("c_other", req.ID(), "yes"); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("wrong conversation err = %v, want 404", err)
	}
	if err := app.Approve("c_agent1", req.ID(), "yes"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(app.PendingApprovals("c_agent1")) != 0 {
		t.Fatal("an answered request must leave the broker")
	}
	info, err := app.Info(ctx, "c_agent1")
	if err != nil {
		t.Fatal(err)
	}
	if info.AgentID != "fake" || info.ModelID != "fake-smart" || info.ProviderID != "" || info.Usage.Total() != 10 {
		t.Fatalf("info = %+v", info)
	}
	msgs, err := app.History(ctx, "c_agent1", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[1].Content() != "found README.md" || msgs[1].ToolCalls()[0].Type() != enum.TooltypeDelegated {
		t.Fatalf("history = %+v", msgs)
	}
	if rt.prompts[0].Model != "fake-smart" {
		t.Fatalf("prompt model = %q", rt.prompts[0].Model)
	}
	want := []enum.TurnEventKind{enum.TurnEventToolCall, enum.TurnEventApproval, enum.TurnEventApprovalResolved, enum.TurnEventDelta, enum.TurnEventFinished}
	if strings.Join(kindStrings(kinds), ",") != strings.Join(kindStrings(want), ",") {
		t.Fatalf("events = %v", kinds)
	}
}

func kindStrings(ks []enum.TurnEventKind) []string {
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = k.String()
	}
	return out
}

func TestAgentPolicyAnswersWithoutAsking(t *testing.T) {
	app, _, _ := agentFixture(t, enum.ApprovalPolicyAutoSafe)
	asked := false
	_, err := app.Send(t.Context(), "c_agent2", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, func(ev event.TurnEvent) {
		if ev.Kind() == enum.TurnEventApproval {
			asked = true
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if asked {
		t.Fatal("auto_safe must answer a read without asking the owner")
	}
}

// An agent keeps its own context, so the footer shows what the agent
// reported: its window as the total and the tokens it holds as live.
func TestAgentContextReportFeedsTheFooter(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	report, err := valueobject.NewAgentContext(53_000, 200_000, 0.0421, "USD")
	if err != nil {
		t.Fatal(err)
	}
	rt.report = &report
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_ctx", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	stats, err := app.ContextFooterStats(ctx, "c_ctx", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 200_000 || stats.Estimate != 0 || stats.Agent.Used() != 53_000 {
		t.Fatalf("footer = total %d estimate %d used %d", stats.Total, stats.Estimate, stats.Agent.Used())
	}
	if amount, currency, ok := stats.Agent.Cost(); !ok || amount != 0.0421 || currency != "USD" {
		t.Fatalf("cost = %v %q %v", amount, currency, ok)
	}

	// The report is stored with the room, so it outlives the process.
	loaded, err := app.deps.Convs.Load(ctx, "c_ctx")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AgentContext().Used() != 53_000 {
		t.Fatalf("stored report = %+v", loaded.AgentContext())
	}

	// Picking a provider model for the next turn measures that model instead.
	other, err := app.ContextFooterStats(ctx, "c_ctx", "gw1", "m1")
	if err != nil {
		t.Fatal(err)
	}
	if !other.Agent.IsZero() {
		t.Fatalf("a provider pick showed the agent's report: %+v", other)
	}
}

func TestAgentRoomWithoutAReportHasNoFooter(t *testing.T) {
	app, _, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_noctx", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	stats, err := app.ContextFooterStats(ctx, "c_noctx", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 0 || !stats.Agent.IsZero() {
		t.Fatalf("an agent that reported nothing produced %+v", stats)
	}
}

// An agent names a thread nobody has named; the owner's rename stays.
func TestAgentNamesTheThread(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	rt.title = "Fixing the login bug"
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_title", "", "", "look at the login", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	info, err := app.Info(ctx, "c_title")
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "Fixing the login bug" {
		t.Fatalf("title = %q", info.Title)
	}

	renamed := "My own name"
	if _, err := app.Annotate(ctx, "c_title", Annotation{Title: &renamed}); err != nil {
		t.Fatal(err)
	}
	rt.title = "The agent's second idea"
	if _, err := app.Send(ctx, "c_title", "", "", "and again", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	if info, _ = app.Info(ctx, "c_title"); info.Title != "My own name" {
		t.Fatalf("the agent renamed the owner's title: %q", info.Title)
	}
}

// Input an agent cannot take mid-turn asks it to stop at a safe point, so
// the input does not wait behind all the agent's remaining work.
func TestMissedSteerYieldsTheTurn(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	rt.yielded = make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	mailbox := appjob.NewSteeringInput()
	var receipt *appjob.SteeringReceipt
	onEvent := func(ev event.TurnEvent) {
		if ev.Kind() == enum.TurnEventToolCall && receipt == nil {
			receipt = mailbox.Submit(valueobject.UserInput{Text: "go left"})
		}
	}
	if _, err := app.Send(ctx, "c_yield", "", "", "wait for yield", nil, appjob.SendOptions{Agent: "fake", Steering: mailbox}, onEvent); err != nil {
		t.Fatal(err)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.yields != 1 {
		t.Fatalf("the agent was asked to yield %d times, want once", rt.yields)
	}
	if len(rt.prompts) != 2 || rt.prompts[1].Input.Content() != "go left" {
		t.Fatalf("the steered input did not start the next prompt: %d prompts", len(rt.prompts))
	}
}

// Steering submitted while the agent is still being set up must not fire
// against a turn that is not on the wire yet: it waits for the prompt to
// be sent, then misses cleanly into the yield path — every input stays in
// order and nothing claims the starting turn.
func TestSteerAcceptedBeforeACPStarts(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	rt.yielded = make(chan struct{})
	opening := make(chan struct{})
	sendPrompt := make(chan struct{})
	rt.beforePrompt = func(ctx context.Context) error {
		select {
		case <-sendPrompt:
			return nil
		default:
		}
		close(opening)
		select {
		case <-sendPrompt:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	mailbox := appjob.NewSteeringInput()
	done := make(chan error, 1)
	go func() {
		_, err := app.Send(ctx, "c_prestart", "", "", "wait for yield", nil, appjob.SendOptions{Agent: "fake", Steering: mailbox}, nil)
		done <- err
	}()
	select {
	case <-opening:
	case <-ctx.Done():
		t.Fatal("the prompt never reached the agent")
	}
	r1 := mailbox.Submit(valueobject.UserInput{Text: "go left"})
	ref, err := valueobject.NewReference("conversation", "c_dog", "@dog", 10, 14)
	if err != nil {
		t.Fatal(err)
	}
	att, err := valueobject.NewAttachment("file:///tmp/steer-note.png", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	r2 := mailbox.Submit(valueobject.UserInput{
		Text:        "slow down @dog",
		References:  []valueobject.Reference{ref},
		Attachments: []valueobject.Attachment{att},
	})
	select {
	case <-rt.yielded:
		t.Fatal("steering ran against a prompt the agent had not taken yet")
	case <-time.After(100 * time.Millisecond):
	}
	if r1.State() != appjob.SteeringPending || r2.State() != appjob.SteeringPending {
		t.Fatalf("receipts = %v,%v, want pending", r1.State(), r2.State())
	}
	close(sendPrompt)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.yields != 1 {
		t.Fatalf("the agent was asked to yield %d times, want once", rt.yields)
	}
	if len(rt.prompts) != 2 || rt.prompts[1].Input.Content() != "go left\n\nslow down @dog" {
		t.Fatalf("the follow-up did not join both inputs: %d prompts", len(rt.prompts))
	}
	if refs := rt.prompts[1].Input.References(); len(refs) != 1 || refs[0].Start() != 19 || refs[0].End() != 23 || refs[0].ID() != "c_dog" {
		t.Fatalf("follow-up references = %v, want @dog shifted to 19:23", refs)
	}
	if atts := rt.prompts[1].Input.Attachments(); len(atts) != 1 || atts[0].URI() != "file:///tmp/steer-note.png" {
		t.Fatalf("follow-up attachments = %v, want the steered upload", atts)
	}
	if r1.State() != appjob.SteeringPersisted || r2.State() != appjob.SteeringPersisted {
		t.Fatalf("receipts = %v,%v, want persisted", r1.State(), r2.State())
	}
}

// An agent's default session options apply to a room that picked none; what
// the room picked itself overrides them.
func TestAgentDefaultsApplyUnderTheRoomsPicks(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	agent, err := app.deps.Agents.Load(ctx, "fake")
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.SetDefaults(valueobject.ConfigSelections{{ID: "mode", Value: "plan"}, {ID: "effort", Value: "high"}}); err != nil {
		t.Fatal(err)
	}
	if err := app.deps.Agents.Save(ctx, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Send(ctx, "c_defaults", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SetAgentOption(ctx, "c_defaults", "mode", "build"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Send(ctx, "c_defaults", "", "", "again", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	prompts := rt.Prompts()
	mode, _ := prompts[0].Options.Value("mode")
	effort, _ := prompts[0].Options.Value("effort")
	if mode != "plan" || effort != "high" {
		t.Fatalf("first turn options = %v", prompts[0].Options)
	}
	mode, _ = prompts[1].Options.Value("mode")
	effort, _ = prompts[1].Options.Value("effort")
	if mode != "build" || effort != "high" {
		t.Fatalf("second turn options = %v", prompts[1].Options)
	}
	// The room pick replaces the default at its own position — not
	// appended, and not duplicated.
	if got := prompts[1].Options; len(got) != 2 || got[0] != (valueobject.ConfigSelection{ID: "mode", Value: "build"}) || got[1] != (valueobject.ConfigSelection{ID: "effort", Value: "high"}) {
		t.Fatalf("merged picks = %v", got)
	}
}

// A session option the agent refuses is not recorded. Storing it would trap the
// owner: every later turn re-applies a value the agent rejects, so the turn
// fails before any newer choice could matter.
func TestRefusedAgentOptionIsNotStored(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_opt", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	rt.mu.Lock()
	rt.refuseValue = "ask" // offered, so validation passes and the agent's refusal is what must refuse it
	rt.mu.Unlock()

	_, err := app.SetAgentOption(ctx, "c_opt", "mode", "ask")
	if code := apperror.As(err).StatusCode(); code != 502 {
		t.Fatalf("a refused option = %v (status %d), want the agent's 502", err, code)
	}
	conv, err := app.deps.Convs.Load(ctx, "c_opt")
	if err != nil {
		t.Fatal(err)
	}
	if picks := conv.AgentOptions(); len(picks) != 0 {
		t.Fatalf("the refused pick was stored: %v", picks)
	}
	// The next turn runs the value that worked, not the refused one.
	if _, err := app.Send(ctx, "c_opt", "", "", "again", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	prompts := rt.Prompts()
	if got, _ := prompts[len(prompts)-1].Options.Value("mode"); got == "ask" {
		t.Fatalf("the next turn applied the refused value: %q", got)
	}
	// A value the agent takes is still stored — the reorder must not turn
	// every pick into a no-op.
	rt.mu.Lock()
	rt.refuseValue = ""
	rt.mu.Unlock()
	if _, err := app.SetAgentOption(ctx, "c_opt", "mode", "build"); err != nil {
		t.Fatal(err)
	}
	conv, err = app.deps.Convs.Load(ctx, "c_opt")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := conv.AgentOptions().Value("mode"); got != "build" {
		t.Fatalf("an accepted pick = %q, want build recorded", got)
	}
}

// A changed session option changes the room, so every open tab hears it — the
// same notice a title or a fork sends. Without it "one state across tabs"
// cannot hold: the other tab keeps showing the option it loaded.
func TestAgentOptionAnnouncesTheChangedRoom(t *testing.T) {
	app, _, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_opt", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var changed []string
	app.OnChanged(func(id string) { mu.Lock(); changed = append(changed, id); mu.Unlock() })
	if _, err := app.SetAgentOption(ctx, "c_opt", "mode", "build"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(changed) != 1 || changed[0] != "c_opt" {
		t.Fatalf("changed = %v, want one notice for c_opt", changed)
	}
}

// An effort or an upstream is a pick of a provider call, and an agent room has
// none: Annotate refuses it and says why. It stores neither and parks neither,
// idle or busy — the refusal comes before anything is written.
func TestAnnotateRefusesProviderPicksOnAnAgentRoom(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_eff", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	effort, upstream := enum.EffortMax, "Alpha"
	if _, err := app.Annotate(ctx, "c_eff", Annotation{Effort: &effort, Upstream: &upstream}); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("idle annotate = %v, want a 400 refusal", err)
	}
	// The record, not a loaded aggregate: Load replays BindAgent, which
	// strips provider picks from an agent room either way.
	info, err := app.deps.Convs.Info(ctx, "c_eff")
	if err != nil {
		t.Fatal(err)
	}
	if info.Effort != "" || info.Upstream != "" {
		t.Fatalf("agent room stored %q, %q, want neither", info.Effort, info.Upstream)
	}
	release := busyTurn(t, app, rt, "c_eff")
	_, err = pickWhileBusy(t, "a turn ran", func() (bool, error) {
		return app.Annotate(ctx, "c_eff", Annotation{Effort: &effort, Upstream: &upstream})
	})
	release()
	if apperror.As(err).StatusCode() != 400 {
		t.Fatalf("busy annotate = %v, want a 400 refusal", err)
	}
	if _, ok, _ := app.Pending(ctx, "c_eff"); ok {
		t.Fatal("a refused pick was parked anyway")
	}
}

// The picker names a provider's effort in the same request as the provider, and
// an effort alone lands on whichever runtime the next turn will run. So a provider
// pick parks with its effort, an effort alone parks while a provider binding
// waits, and the same effort is refused once an agent binding waits.
func TestAProviderPickCarriesItsEffortPastAnAgentRoom(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_pp", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	effort := enum.EffortMax
	release := busyTurn(t, app, rt, "c_pp")
	defer release()
	parked, err := pickWhileBusy(t, "a turn ran", func() (bool, error) {
		return app.Annotate(ctx, "c_pp", Annotation{Runtime: &RuntimeBinding{ProviderID: "gw1", ModelID: "m1"}, Effort: &effort})
	})
	if err != nil || !parked {
		t.Fatalf("provider pick with its effort = %v, %v; want parked", parked, err)
	}
	parked, err = pickWhileBusy(t, "a turn ran", func() (bool, error) {
		return app.Annotate(ctx, "c_pp", Annotation{Effort: &effort})
	})
	if err != nil || !parked {
		t.Fatalf("effort alone under a waiting provider = %v, %v; want parked", parked, err)
	}
	if _, err := pickWhileBusy(t, "a turn ran", func() (bool, error) {
		return app.Annotate(ctx, "c_pp", Annotation{Runtime: &RuntimeBinding{AgentID: "fake", AgentModel: "swe-2-1"}})
	}); err != nil {
		t.Fatalf("agent binding = %v; want parked", err)
	}
	if _, err := pickWhileBusy(t, "a turn ran", func() (bool, error) {
		return app.Annotate(ctx, "c_pp", Annotation{Effort: &effort})
	}); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("effort under a waiting agent = %v, want a 400 refusal", err)
	}
}

// unreadablePendingRepo fails the read of a room's waiting pick once broken is set,
// as a store that cannot read the file does. Everything else passes through.
type unreadablePendingRepo struct {
	drepo.Conversation
	broken bool
}

func (r *unreadablePendingRepo) PendingSelection(ctx context.Context, id string) (drepo.PendingSelection, bool, error) {
	if r.broken {
		return drepo.PendingSelection{}, false, fmt.Errorf("pending selection corrupt")
	}
	return r.Conversation.PendingSelection(ctx, id)
}

// A write that would supersede a parked pick is refused before it saves when that
// pick cannot be read: the save would land and the unreadable generation would
// survive it unseen. The room keeps the values it had.
func TestWriteOverAnUnreadablePendingIsRefusedBeforeSave(t *testing.T) {
	var store *unreadablePendingRepo
	app, _ := fixtureWith(t, okHandler, func(c drepo.Conversation) drepo.Conversation {
		store = &unreadablePendingRepo{Conversation: c}
		return store
	})
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_bad", "gw1", "m1", "hi", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	store.broken = true
	effort, upstream := enum.EffortMax, "Alpha"
	if _, err := app.Annotate(ctx, "c_bad", Annotation{Effort: &effort, Upstream: &upstream}); apperror.As(err).StatusCode() != 500 {
		t.Fatalf("write over an unreadable pick = %v, want a 500 refusal", err)
	}
	info, err := app.deps.Convs.Info(ctx, "c_bad")
	if err != nil {
		t.Fatal(err)
	}
	if info.Effort != "" || info.Upstream != "" {
		t.Fatalf("refused write saved %q, %q; want the room unchanged", info.Effort, info.Upstream)
	}
}

// A provider room stores both, exactly as before.
func TestAnnotateStoresProviderPicksOnAProviderRoom(t *testing.T) {
	app, _ := fixture(t, okHandler)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_eff", "gw1", "m1", "hi", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	effort, upstream := enum.EffortMax, "Alpha"
	if _, err := app.Annotate(ctx, "c_eff", Annotation{Effort: &effort, Upstream: &upstream}); err != nil {
		t.Fatal(err)
	}
	info, err := app.deps.Convs.Info(ctx, "c_eff")
	if err != nil {
		t.Fatal(err)
	}
	if info.Effort != string(enum.EffortMax) || info.Upstream != "Alpha" {
		t.Fatalf("provider room stored %q, %q", info.Effort, info.Upstream)
	}
}

// A room that just restarted has no live session, so nothing applied the pick —
// the catalog is the only thing that can refuse it. The scripted runtime's
// SetOption applies nothing, which is what a room without a session does.
func TestAgentOptionIsCheckedAgainstTheCatalogWithoutALiveSession(t *testing.T) {
	app, _, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_opt", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}

	for _, pick := range [][2]string{
		{"turbo", "fast"}, // no such option
		{"mode", "nope"},  // the option exists, the value does not
		{"strict", "yes"}, // a boolean takes true or false
	} {
		_, err := app.SetAgentOption(ctx, "c_opt", pick[0], pick[1])
		if code := apperror.As(err).StatusCode(); code != 400 {
			t.Fatalf("%v = %v (status %d), want 400", pick, err, code)
		}
	}
	info, err := app.deps.Convs.Info(ctx, "c_opt")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.AgentOptions) != 0 {
		t.Fatalf("refused picks were stored: %v", info.AgentOptions)
	}
	// What the catalog does offer is recorded, still with no session.
	if _, err := app.SetAgentOption(ctx, "c_opt", "mode", "build"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SetAgentOption(ctx, "c_opt", "strict", "true"); err != nil {
		t.Fatal(err)
	}
	info, err = app.deps.Convs.Info(ctx, "c_opt")
	if err != nil {
		t.Fatal(err)
	}
	mode, _ := info.AgentOptions.Value("mode")
	strict, _ := info.AgentOptions.Value("strict")
	if mode != "build" || strict != "true" {
		t.Fatalf("stored picks = %v", info.AgentOptions)
	}
}

// The picker never draws a current value outside its own choices, even when an
// older bug left one in the store.
func TestAgentOptionsNeverReportACurrentOutsideItsChoices(t *testing.T) {
	app, _, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_opt", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	// Written through the annotation job, the way a build before this fix did.
	poison := [2]string{"mode", "nope"}
	if _, err := app.Annotate(ctx, "c_opt", Annotation{AgentOption: &poison}); err != nil {
		t.Fatal(err)
	}
	opts, _, err := app.AgentOptions(ctx, "c_opt")
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range opts {
		if o.Type == "select" && !slices.ContainsFunc(o.Choices, func(c dservice.OptionChoice) bool { return c.Value == o.Current }) {
			t.Fatalf("option %q reports current %q, which it does not offer: %+v", o.ID, o.Current, o.Choices)
		}
	}
	for _, o := range opts {
		if o.ID == "mode" && o.Current != "ask" {
			t.Fatalf("mode current = %q, want the catalog's own ask", o.Current)
		}
	}
}

// An agent has no edit of its own, so editing a message in an agent thread
// drops the turns after it, forgets the agent's session, and gives the agent
// the edited line afresh; retrying hands it a message that never got a reply.
func TestAgentThreadEditAndRetry(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_edit", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Send(ctx, "c_edit", "", "", "and then", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	msgs, _ := app.History(ctx, "c_edit", 0, 10)
	if len(msgs) != 4 {
		t.Fatalf("history = %d messages", len(msgs))
	}

	// The web sends no runtime with an edit; the thread's own agent runs it.
	if err := app.Edit(ctx, "c_edit", "", "", msgs[0].ID(), "look closer", appjob.SendOptions{}, nil); err != nil {
		t.Fatalf("edit: %v", err)
	}
	prompts := rt.Prompts()
	last := prompts[len(prompts)-1]
	if last.Input.Content() != "look closer" || rt.sessions[len(rt.sessions)-1] != "" {
		t.Fatalf("edit prompt = %q on session %q", last.Input.Content(), rt.sessions[len(rt.sessions)-1])
	}
	msgs, _ = app.History(ctx, "c_edit", 0, 10)
	if len(msgs) != 2 || msgs[0].Content() != "look closer" || msgs[1].Role() != enum.RoleAssistant {
		t.Fatalf("history after the edit = %d messages, first %q", len(msgs), msgs[0].Content())
	}

	// A reply the agent already gave is not retried.
	err := app.Resume(ctx, "c_edit", "", "", appjob.SendOptions{}, nil)
	if apperror.As(err).StatusCode() != 409 {
		t.Fatalf("retry of an answered turn: %v", err)
	}
	// A message that never got one is.
	unanswered, _ := entity.NewMessage(enum.RoleUser, "are you there")
	if _, err := app.deps.Convs.AppendMessage(ctx, "c_edit", unanswered); err != nil {
		t.Fatal(err)
	}
	before := len(rt.Prompts())
	if err := app.Resume(ctx, "c_edit", "", "", appjob.SendOptions{}, nil); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if prompts = rt.Prompts(); len(prompts) != before+1 || prompts[before].Input.Content() != "are you there" {
		t.Fatalf("retry did not hand the message to the agent: %d prompts", len(prompts))
	}
}

// An edit rewinds the room, so the agent's old session is no longer the
// room's: it is removed once the agent has a new one, not before — and never
// when the agent hands back the id it had, which would remove the new session.
func TestEditRemovesTheOldAgentSession(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	rt.rotate = true
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_retire", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	before, _ := app.Info(ctx, "c_retire")
	old := before.AgentSession
	if old == "" {
		t.Fatal("the room holds no agent session before the edit")
	}
	msgs, _ := app.History(ctx, "c_retire", 0, 10)
	if err := app.Edit(ctx, "c_retire", "", "", msgs[0].ID(), "look closer", appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	info, _ := app.Info(ctx, "c_retire")
	if info.AgentSession == old || info.AgentSession == "" {
		t.Fatalf("the edit should have opened a new session, room holds %q", info.AgentSession)
	}
	if got := rt.retiredSessions(t, 1); len(got) != 1 || got[0] != "fake/"+old {
		t.Fatalf("retired = %v, want only %q", got, "fake/"+old)
	}
}

func TestEditDoesNotRemoveASessionTheAgentReused(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll) // one id, every time
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_reuse", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	msgs, _ := app.History(ctx, "c_reuse", 0, 10)
	if err := app.Edit(ctx, "c_reuse", "", "", msgs[0].ID(), "look closer", appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.retired) != 0 {
		t.Fatalf("removed %v, which is the session the room still holds", rt.retired)
	}
}

// Moving a room to another agent, or back to a provider, leaves the old
// agent's session behind; it is removed, from the agent that issued it, once
// the turn on the new runtime has succeeded.
func TestChangingRuntimeRemovesTheOldAgentSession(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	rt.rotate = true
	ctx := t.Context()
	other, _ := aggregate.NewAgent("other", "Other", "other-acp", nil, enum.ApprovalPolicyAutoAll)
	if err := app.deps.Agents.Save(ctx, &other); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Send(ctx, "c_move", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	before, _ := app.Info(ctx, "c_move")
	first := before.AgentSession
	// To another agent.
	if _, err := app.Send(ctx, "c_move", "", "", "carry on", nil, appjob.SendOptions{Agent: "other"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := rt.retiredSessions(t, 1); len(got) != 1 || got[0] != "fake/"+first {
		t.Fatalf("after the change of agent: %v", got)
	}
	info, _ := app.Info(ctx, "c_move")
	second := info.AgentSession
	// Back to a provider.
	if _, err := app.Send(ctx, "c_move", "gw1", "m1", "and now", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := rt.retiredSessions(t, 2); len(got) != 2 || got[1] != "other/"+second {
		t.Fatalf("after the change to a provider: %v", got)
	}
}

func TestRoomSwitchesBetweenAgentAndProvider(t *testing.T) {
	app, rt, bodies := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_switch", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	// An edit in an agent room drops the later turns and the agent's
	// session; the agent answers the edited line afresh.
	msgs, _ := app.History(ctx, "c_switch", 0, 10)
	if err := app.Edit(ctx, "c_switch", "", "", msgs[0].ID(), "other", appjob.SendOptions{}, nil); err != nil {
		t.Fatalf("edit err = %v", err)
	}
	prompts := rt.Prompts()
	if last, session := prompts[len(prompts)-1], rt.sessions[len(rt.sessions)-1]; last.Input.Content() != "other" || session != "" {
		t.Fatalf("edit prompt = %q, session %q", last.Input.Content(), session)
	}
	if msgs, _ = app.History(ctx, "c_switch", 0, 10); msgs[0].Content() != "other" || msgs[len(msgs)-1].Role() != enum.RoleAssistant {
		t.Fatalf("history after edit = %d messages, first %q", len(msgs), msgs[0].Content())
	}

	// Move to the provider: the model replays the agent's work as text.
	if _, err := app.Send(ctx, "c_switch", "gw1", "m1", "summarize", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	info, _ := app.Info(ctx, "c_switch")
	if info.AgentID != "" || info.ProviderID != "gw1" || info.ModelID != "m1" {
		t.Fatalf("after switch to provider: %+v", info)
	}
	var req struct {
		Messages []struct {
			Content   any   `json:"content"`
			ToolCalls []any `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte((*bodies)[0]), &req); err != nil {
		t.Fatal(err)
	}
	replayed := false
	for _, m := range req.Messages {
		for _, tc := range m.ToolCalls {
			if raw, _ := json.Marshal(tc); strings.Contains(string(raw), "Read files") {
				t.Fatalf("an agent's call reached the model as a tool call: %s", raw)
			}
		}
		if s, ok := m.Content.(string); ok && strings.Contains(s, "[agent tool: Read files]") {
			replayed = true
		}
	}
	if !replayed {
		t.Fatalf("the agent's work is missing from the model request: %s", (*bodies)[0])
	}

	// Back to the agent: it never saw the provider turn, so it starts fresh.
	if _, err := app.Send(ctx, "c_switch", "", "", "again", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := rt.sessions[1]; got != "" {
		t.Fatalf("returning to the agent reused session %q", got)
	}

	if err := app.Delete(ctx, "c_switch"); err != nil {
		t.Fatal(err)
	}
	// The agent's own record of the room goes with it. Removing it can mean
	// starting the agent, so it happens after the delete has answered.
	deadline := time.Now().Add(2 * time.Second)
	for {
		rt.mu.Lock()
		got := slices.Clone(rt.discarded)
		rt.mu.Unlock()
		// One removal when the room moved to the provider, which left the
		// agent's session behind, and one when the room itself was deleted.
		if len(got) == 2 && got[0] == "c_switch" && got[1] == "c_switch" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("discarded = %v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// failingCatalog is the AgentCatalogStore that cannot answer: a pick that
// needs it must refuse the write rather than guess at what the agent once
// offered.
type failingCatalog struct{}

func (failingCatalog) Catalog(context.Context, string) (drepo.AgentCatalog, error) {
	return drepo.AgentCatalog{}, fmt.Errorf("catalog unreadable")
}
func (failingCatalog) SaveCatalog(context.Context, string, drepo.AgentCatalog) error {
	return fmt.Errorf("catalog unwritable")
}

// D1: a pick for an id the stored catalog knows but the session does not
// offer is stored deferred — and the DTO shows it marked, apart from current.
func TestSetAgentOptionStoresAKnownUnofferedPickAsDeferred(t *testing.T) {
	cat := repoagent.New(t.TempDir(), nil)
	if err := cat.SaveCatalog(t.Context(), "fake", drepo.AgentCatalog{Options: []dservice.AgentOption{
		{ID: "effort", Name: "Reasoning effort", Category: "thought_level", Type: "select", Current: "low",
			Choices: []dservice.OptionChoice{{Value: "low"}, {Value: "high"}}},
	}}); err != nil {
		t.Fatal(err)
	}
	app, _, _ := agentFixture(t, enum.ApprovalPolicyAutoAll, func(app *ConversationApp) {
		app.deps.AgentCatalog = cat
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := app.Send(ctx, "c_opt", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SetAgentOption(ctx, "c_opt", "effort", "high"); err != nil {
		t.Fatalf("a known-but-unoffered pick was refused: %v", err)
	}
	list, _, err := app.AgentOptions(ctx, "c_opt")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(list, func(o dservice.AgentOption) bool { return o.ID == "effort" })
	if i < 0 || list[i].Deferred != "high" || list[i].Current != "" {
		t.Fatalf("deferred entry = %+v, want effort marked deferred=high with empty current", list)
	}
	if list[i].Name != "Reasoning effort" {
		t.Fatalf("the stored catalog's name was not kept: %+v", list[i])
	}
}

// D1: an id no catalog has ever seen is refused — nothing is stored.
func TestSetAgentOptionRefusesAnIdNoCatalogKnows(t *testing.T) {
	app, _, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := app.Send(ctx, "c_opt", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	_, err := app.SetAgentOption(ctx, "c_opt", "nosuch", "x")
	if err == nil || apperror.As(err).StatusCode() != 400 {
		t.Fatalf("unknown pick err = %v, want 400", err)
	}
}

// D1: a deferred candidate whose value the stored catalog does not list is
// refused the same way an offered option refuses it.
func TestSetAgentOptionRefusesAValueOutsideTheStoredChoices(t *testing.T) {
	cat := repoagent.New(t.TempDir(), nil)
	if err := cat.SaveCatalog(t.Context(), "fake", drepo.AgentCatalog{Options: []dservice.AgentOption{
		{ID: "effort", Name: "Effort", Type: "select", Current: "low",
			Choices: []dservice.OptionChoice{{Value: "low"}, {Value: "high"}}},
	}}); err != nil {
		t.Fatal(err)
	}
	app, _, _ := agentFixture(t, enum.ApprovalPolicyAutoAll, func(app *ConversationApp) {
		app.deps.AgentCatalog = cat
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := app.Send(ctx, "c_opt", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	_, err := app.SetAgentOption(ctx, "c_opt", "effort", "max")
	if err == nil || apperror.As(err).StatusCode() != 400 {
		t.Fatalf("out-of-choices deferred pick err = %v, want 400", err)
	}
}

// D1: when the catalog cannot be read the write refuses with 503 — an
// unverified pick is never stored.
func TestSetAgentOptionRefusesWhenTheCatalogCannotBeRead(t *testing.T) {
	app, _, _ := agentFixture(t, enum.ApprovalPolicyAutoAll, func(app *ConversationApp) {
		app.deps.AgentCatalog = failingCatalog{}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := app.Send(ctx, "c_opt", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	_, err := app.SetAgentOption(ctx, "c_opt", "nosuch", "x")
	if err == nil || apperror.As(err).StatusCode() != 503 {
		t.Fatalf("unreadable catalog err = %v, want 503", err)
	}
}

// The deferred mark is derived, not stored: when the session offers the
// option again the entry rejoins the list and the pick reads as its value.
func TestDeferredPickRejoinsTheOfferedListWhenItMaterializes(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := app.Send(ctx, "c_opt", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	// Stored directly: a pick made while "effort" was still offered.
	if _, err := app.Annotate(ctx, "c_opt", Annotation{AgentOption: &[2]string{"effort", "high"}}); err != nil {
		t.Fatal(err)
	}
	list, _, err := app.AgentOptions(ctx, "c_opt")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(list, func(o dservice.AgentOption) bool { return o.ID == "effort" })
	if i < 0 || list[i].Deferred != "high" {
		t.Fatalf("unoffered stored pick did not show deferred: %+v", list)
	}
	// The option materializes — the next read finds it offered again, the
	// pick as its value, and no deferred entry left.
	rt.mu.Lock()
	rt.options = []dservice.AgentOption{
		{ID: "effort", Name: "Effort", Type: "select", Current: "low",
			Choices: []dservice.OptionChoice{{Value: "low"}, {Value: "high"}}},
	}
	rt.mu.Unlock()
	list, _, err = app.AgentOptions(ctx, "c_opt")
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(list, func(o dservice.AgentOption) bool { return o.Deferred != "" }) {
		t.Fatalf("a deferred entry survived materialization: %+v", list)
	}
	if list[0].Current != "high" {
		t.Fatalf("materialized option shows current = %q, want the pick", list[0].Current)
	}
}

// A provider room refuses agent-option picks the same way it always did —
// the domain rule answers before any catalog is consulted, so a catalog
// failing on every read cannot change the outcome.
func TestSetAgentOptionOnAProviderRoomSkipsTheCatalog(t *testing.T) {
	app, _ := fixture(t, okHandler)
	app.deps.AgentCatalog = failingCatalog{}
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_prov", "gw1", "m1", "hi", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	_, err := app.SetAgentOption(ctx, "c_prov", "anything", "x")
	if err == nil || !strings.Contains(err.Error(), "bound to an agent") {
		t.Fatalf("provider room refusal changed: %v", err)
	}
}
