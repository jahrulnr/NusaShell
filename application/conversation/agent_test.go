package appconversation

import (
	"context"
	"encoding/json"
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
	steered   chan string
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
func (a *scriptedAgent) Options(context.Context, *aggregate.Agent, string) ([]dservice.AgentOption, error) {
	return nil, nil
}
func (a *scriptedAgent) SetOption(context.Context, string, string, string) error { return nil }
func (a *scriptedAgent) Commands(string) []dservice.AgentCommand                 { return nil }
func (a *scriptedAgent) Status(string) dservice.AgentStatus                      { return dservice.AgentStatus{} }
func (a *scriptedAgent) Steer(_ context.Context, _ string, input valueobject.UserInput, delivered func()) bool {
	if !a.steerable {
		return false
	}
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
		append([]Option{WithAgents(agents, rt)}, opts...)...,
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
	if err := app.Annotate(ctx, "c_title", Annotation{Title: &renamed}); err != nil {
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
	if err := agent.SetDefaults(map[string]string{"mode": "plan", "effort": "high"}); err != nil {
		t.Fatal(err)
	}
	if err := app.deps.Agents.Save(ctx, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Send(ctx, "c_defaults", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := app.SetAgentOption(ctx, "c_defaults", "mode", "build"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Send(ctx, "c_defaults", "", "", "again", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	prompts := rt.Prompts()
	if first := prompts[0].Options; first["mode"] != "plan" || first["effort"] != "high" {
		t.Fatalf("first turn options = %v", first)
	}
	if second := prompts[1].Options; second["mode"] != "build" || second["effort"] != "high" {
		t.Fatalf("second turn options = %v", second)
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
