package job

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	repoconversation "github.com/jahrulnr/nusashell/infrastructure/repository/conversation"
)

// steerFoldSvc asks for the "push" tool on the first provider call and
// answers "done" on the second, recording the last two message contents
// it saw — proving where the folded steer sits relative to the tool
// result and the workspace record that trails it.
type steerFoldSvc struct {
	calls    atomic.Int32
	lastSeen string
	prevSeen string
}

func (s *steerFoldSvc) Send(_ context.Context, conv *aggregate.Conversation, _ *aggregate.Provider, _ entity.Model, _ event.TurnEventHandler) error {
	n := s.calls.Add(1)
	var m entity.Message
	if n == 1 {
		m, _ = entity.NewMessage(enum.RoleAssistant, "need a tool")
		tc, err := entity.NewToolCall("call_1", "push", `{}`, enum.TooltypeOrigin)
		if err != nil {
			return err
		}
		m.AddToolCall(tc)
		m.SetFinishReason(enum.FinishReasonToolCalls)
	} else {
		msgs := conv.Messages()
		s.lastSeen = msgs[len(msgs)-1].Content()
		s.prevSeen = msgs[len(msgs)-2].Content()
		m, _ = entity.NewMessage(enum.RoleAssistant, "done")
		m.SetFinishReason(enum.FinishReasonStop)
	}
	conv.Append(m)
	return nil
}

// pushTool answers instantly; its onExec hook runs inside the turn's
// tool phase, which is exactly when a mid-run steer is delivered.
type pushTool struct {
	schema valueobject.ToolSchema
	onExec func()
}

func (t pushTool) Schema() valueobject.ToolSchema { return t.schema }
func (t pushTool) Execute(context.Context, entity.ToolCall, dservice.ToolEmitter) (dservice.ToolResult, error) {
	if t.onExec != nil {
		t.onExec()
	}
	return dservice.ToolResult{Text: "tool output"}, nil
}

// A steer pushed while a tool executes folds at the top of the next
// step — after the tool result persisted, before the second provider
// call — as an ordinary user message with a steer event.
func TestRunTurnFoldsSteerAfterToolResult(t *testing.T) {
	d := Deps{
		SummaryTool: mustSummaryTool(t),
		Convs:       repoconversation.NewConversationRepository(t.TempDir()),
		Config:      config.ConversationConfig{MaxTurnStep: 4},
	}
	conv, _ := aggregate.NewConversation("room")
	user, _ := entity.NewMessage(enum.RoleUser, "go")
	conv.Append(user)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Convs.AppendMessage(t.Context(), "room", user); err != nil {
		t.Fatal(err)
	}
	steers := NewSteeringInput()
	schema, _ := valueobject.NewToolSchema("push", "test tool", nil, true, enum.ToolPermissionAgent)
	driver := EnvironmentBundle{Tools: map[string]dservice.Tool{
		"push": pushTool{schema: schema, onExec: func() { steers.push("also handle X") }},
	}}
	svc := &steerFoldSvc{}
	var kinds []enum.TurnEventKind
	onEvent := func(e event.TurnEvent) { kinds = append(kinds, e.Kind()) }
	if err := d.runTurn(t.Context(), "room", conv, driver, nil, entity.Model{}, svc, onEvent, steers, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	msgs := conv.Messages()
	// user "go" / workspace record / assistant+push call / folded steer /
	// assistant "done". The steer is followed by no second record: the pin
	// after "go" still cites the same directory, so repeating it would
	// spend tokens without telling the model anything new.
	if len(msgs) != 5 {
		t.Fatalf("folded history = %d msgs, want 5", len(msgs))
	}
	if msgs[3].Role() != enum.RoleUser || msgs[3].Content() != "also handle X" {
		t.Fatalf("steer message = %v %q", msgs[3].Role(), msgs[3].Content())
	}
	if !msgs[2].ToolCalls()[0].HasOutput() {
		t.Fatal("the tool result must be recorded before the steer folded")
	}
	requireWorkspaceContext(t, msgs[1], "")
	// The second provider call saw the steer as the tail; the live pin
	// after "go" is still the citation the wire carries for this dir.
	if svc.prevSeen != "need a tool" || svc.lastSeen != "also handle X" {
		t.Fatalf("provider call 2 saw prev=%q last=%q, want the tool turn then the steer", svc.prevSeen, svc.lastSeen)
	}
	if !slices.Contains(kinds, enum.TurnEventSteer) {
		t.Fatalf("no steer event emitted: %v", kinds)
	}
	stored, err := d.Convs.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Messages()) != 5 || stored.Messages()[3].Content() != "also handle X" {
		t.Fatalf("persisted history missing the fold: %d msgs", len(stored.Messages()))
	}
}

// captureSvc records the tail the provider call saw — the last two
// message contents plus the system prompt — used to prove what sat in
// history at call time.
type captureSvc struct {
	lastSeen  string
	prevSeen  string
	sawPrompt string
}

func (s *captureSvc) Send(_ context.Context, conv *aggregate.Conversation, _ *aggregate.Provider, _ entity.Model, _ event.TurnEventHandler) error {
	msgs := conv.Messages()
	s.lastSeen = msgs[len(msgs)-1].Content()
	if len(msgs) > 1 {
		s.prevSeen = msgs[len(msgs)-2].Content()
	}
	s.sawPrompt = conv.SystemPrompt()
	m, _ := entity.NewMessage(enum.RoleAssistant, "done")
	m.SetFinishReason(enum.FinishReasonStop)
	conv.Append(m)
	return nil
}

// Input posted before the job even starts — a steer queued behind the
// worker's earlier work — folds at step zero, before the first call.
func TestRunTurnFoldsSteerBeforeFirstCall(t *testing.T) {
	d := Deps{
		SummaryTool: mustSummaryTool(t),
		Convs:       repoconversation.NewConversationRepository(t.TempDir()),
		Config:      config.ConversationConfig{MaxTurnStep: 4},
	}
	conv, _ := aggregate.NewConversation("room")
	user, _ := entity.NewMessage(enum.RoleUser, "hi")
	conv.Append(user)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Convs.AppendMessage(t.Context(), "room", user); err != nil {
		t.Fatal(err)
	}
	steers := NewSteeringInput()
	steers.push("early input")
	svc := &captureSvc{}
	if err := d.runTurn(t.Context(), "room", conv, EnvironmentBundle{}, nil, entity.Model{}, svc, nil, steers, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	// Call 1 saw the folded steer with its context record trailing it.
	if svc.prevSeen != "early input" || svc.lastSeen != "" {
		t.Fatalf("provider call 1 saw prev=%q last=%q, want the steer then its context record", svc.prevSeen, svc.lastSeen)
	}
	msgs := conv.Messages()
	if len(msgs) != 4 || msgs[1].Role() != enum.RoleUser || msgs[1].Content() != "early input" {
		t.Fatalf("pre-start steer did not fold: %+v", msgs)
	}
	requireWorkspaceContext(t, msgs[2], "")
}

// doneSvc answers immediately; its onSend hook runs inside the single
// provider call — a steer pushed there has no further step to fold at.
type doneSvc struct{ onSend func() }

func (s doneSvc) Send(_ context.Context, conv *aggregate.Conversation, _ *aggregate.Provider, _ entity.Model, _ event.TurnEventHandler) error {
	if s.onSend != nil {
		s.onSend()
	}
	m, _ := entity.NewMessage(enum.RoleAssistant, "done")
	m.SetFinishReason(enum.FinishReasonStop)
	conv.Append(m)
	return nil
}

// A steer arriving during the turn's last provider call is never
// consumed — it stays in the inbox for the caller to recall or reroute;
// it must not leak into history.
func TestRunTurnLeavesUnconsumedSteer(t *testing.T) {
	d := Deps{
		SummaryTool: mustSummaryTool(t),
		Convs:       repoconversation.NewConversationRepository(t.TempDir()),
		Config:      config.ConversationConfig{MaxTurnStep: 4},
	}
	conv, _ := aggregate.NewConversation("room")
	user, _ := entity.NewMessage(enum.RoleUser, "hi")
	conv.Append(user)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Convs.AppendMessage(t.Context(), "room", user); err != nil {
		t.Fatal(err)
	}
	steers := NewSteeringInput()
	svc := doneSvc{onSend: func() { steers.push("too late") }}
	if err := d.runTurn(t.Context(), "room", conv, EnvironmentBundle{}, nil, entity.Model{}, svc, nil, steers, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages()) != 3 {
		t.Fatalf("unconsumed steer entered history: %+v", conv.Messages())
	}
	if got, ok := steers.popLast(); !ok || got != "too late" {
		t.Fatalf("recall = %q,%v — the caller must get the line back", got, ok)
	}
}

// The mailbox keeps FIFO on claim and LIFO on recall.
func TestSteerInboxFIFOAndRecall(t *testing.T) {
	in := NewSteeringInput()
	in.push("first")
	in.push("second")
	if got, ok := in.popLast(); !ok || got != "second" {
		t.Fatalf("recall pops the newest: %q,%v", got, ok)
	}
	if claimed := in.claim(); len(claimed) != 1 || claimed[0].input.Text != "first" {
		t.Fatalf("claim keeps FIFO of the remainder: %v", claimed)
	}
	if _, ok := in.popLast(); ok {
		t.Fatal("empty mailbox must report nothing to recall")
	}
}

// Exact receipts: recall works only while pending — claimed and
// persisted input answer false, and a recalled receipt stays terminal.
func TestSteeringInputRecallExactPendingOnly(t *testing.T) {
	in := NewSteeringInput()
	a := in.Submit(valueobject.UserInput{Text: "first"})
	b := in.Submit(valueobject.UserInput{Text: "second"})
	if !in.Recall(b) {
		t.Fatal("recall of a pending receipt must succeed")
	}
	if b.State() != SteeringRecalled || a.State() != SteeringPending {
		t.Fatalf("states = %v,%v", a.State(), b.State())
	}
	if in.Recall(b) || in.Recall(nil) {
		t.Fatal("a recalled or nil receipt must refuse recall")
	}
	claimed := in.claim()
	if len(claimed) != 1 || claimed[0].input.Text != "first" || a.State() != SteeringClaimed {
		t.Fatalf("claim leaves only the unrecalled entry: %+v", claimed)
	}
	if in.Recall(a) {
		t.Fatal("claimed input must refuse recall")
	}
	a.markPersisted()
	if in.Recall(a) || a.State() != SteeringPersisted {
		t.Fatal("persisted input must refuse recall")
	}
}

// The CLI's last-in recall withdraws web-submitted input too — the
// shared receipt reflects it, so the transport can report "recalled".
func TestSteeringInputCLIRecallMarksWebReceipt(t *testing.T) {
	in := NewSteeringInput()
	in.push("cli line")
	web := in.Submit(valueobject.UserInput{Text: "web line"})
	if got, ok := in.popLast(); !ok || got != "web line" {
		t.Fatalf("recall = %q,%v", got, ok)
	}
	if web.State() != SteeringRecalled {
		t.Fatalf("web receipt = %v, want recalled", web.State())
	}
}

// scriptedAppendConvs scripts AppendMessage results for the first calls —
// the committed bool and the error independently — then delegates to the
// real repository. The steered fold is the first thing runTurn appends.
type scriptedAppendConvs struct {
	drepo.Conversation
	results []struct {
		committed bool
		err       error
	}
}

func (c *scriptedAppendConvs) AppendMessage(ctx context.Context, convID string, m entity.Message) (bool, error) {
	if len(c.results) > 0 {
		r := c.results[0]
		c.results = c.results[1:]
		return r.committed, r.err
	}
	return c.Conversation.AppendMessage(ctx, convID, m)
}

// seedSteerRoom builds the repo+conv fixture every steer test shares —
// seeded through convs BEFORE it is wrapped in a script, so scripted
// AppendMessage results line up with the steered fold.
func seedSteerRoom(t *testing.T, convs drepo.Conversation) (*aggregate.Conversation, *Deps) {
	t.Helper()
	d := &Deps{Convs: convs, Config: config.ConversationConfig{MaxTurnStep: 4}}
	conv, _ := aggregate.NewConversation("room")
	user, _ := entity.NewMessage(enum.RoleUser, "hi")
	conv.Append(user)
	if err := convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	if _, err := convs.AppendMessage(t.Context(), "room", user); err != nil {
		t.Fatal(err)
	}
	return conv, d
}

// AppendMessage(true, err) means committed despite the post-commit
// error — the receipt is persisted even though the turn fails.
func TestRunTurnSteerAppendCommittedDespiteError(t *testing.T) {
	base := repoconversation.NewConversationRepository(t.TempDir())
	conv, _ := aggregate.NewConversation("room")
	user, _ := entity.NewMessage(enum.RoleUser, "hi")
	conv.Append(user)
	if err := base.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	if _, err := base.AppendMessage(t.Context(), "room", user); err != nil {
		t.Fatal(err)
	}
	convs := &scriptedAppendConvs{Conversation: base}
	convs.results = append(convs.results, struct {
		committed bool
		err       error
	}{true, errors.New("post-commit flush")})
	d := Deps{
		SummaryTool: mustSummaryTool(t), Convs: convs, Config: config.ConversationConfig{MaxTurnStep: 4}}
	steers := NewSteeringInput()
	receipt := steers.Submit(valueobject.UserInput{Text: "steer me"})
	err := d.runTurn(t.Context(), "room", conv, EnvironmentBundle{}, nil, entity.Model{}, doneSvc{}, nil, steers, valueobject.TurnOrigin{})
	if err == nil {
		t.Fatal("the post-commit error must still fail the turn")
	}
	if receipt.State() != SteeringPersisted {
		t.Fatalf("receipt = %v, want persisted — the bool decides, not the error", receipt.State())
	}
}

// AppendMessage(false, err) means unconsumed — the claimed-but-
// uncommitted entry and the rest of the drained batch stay claimed so
// the terminal snapshot reports them unconsumed.
func TestRunTurnSteerAppendFailureLeavesUnconsumed(t *testing.T) {
	base := repoconversation.NewConversationRepository(t.TempDir())
	conv, _ := seedSteerRoom(t, base)
	convs := &scriptedAppendConvs{Conversation: base}
	convs.results = append(convs.results, struct {
		committed bool
		err       error
	}{false, errors.New("disk gone")})
	d := Deps{
		SummaryTool: mustSummaryTool(t), Convs: convs, Config: config.ConversationConfig{MaxTurnStep: 4}}
	steers := NewSteeringInput()
	failed := steers.Submit(valueobject.UserInput{Text: "first line"})
	drained := steers.Submit(valueobject.UserInput{Text: "second line"})
	err := d.runTurn(t.Context(), "room", conv, EnvironmentBundle{}, nil, entity.Model{}, doneSvc{}, nil, steers, valueobject.TurnOrigin{})
	if err == nil {
		t.Fatal("the failed append must fail the turn")
	}
	// both were detached in the same claim — neither ever committed
	if failed.State() != SteeringClaimed || drained.State() != SteeringClaimed {
		t.Fatalf("unconsumed receipts = %v,%v, want claimed,claimed", failed.State(), drained.State())
	}
}

// CLI pushes and web Submits carrying identical text fold identically —
// both become user lines, and the web receipt turns persisted.
func TestRunTurnSteerIdenticalTextBothPaths(t *testing.T) {
	conv, d := seedSteerRoom(t, repoconversation.NewConversationRepository(t.TempDir()))
	steers := NewSteeringInput()
	steers.push("same text")
	receipt := steers.Submit(valueobject.UserInput{Text: "same text"})
	if err := d.runTurn(t.Context(), "room", conv, EnvironmentBundle{}, nil, entity.Model{}, doneSvc{}, nil, steers, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	msgs := conv.Messages()
	if len(msgs) != 5 || msgs[1].Content() != "same text" || msgs[2].Content() != "same text" {
		t.Fatalf("identical steer lines must both fold: %+v", msgs)
	}
	if receipt.State() != SteeringPersisted {
		t.Fatalf("web receipt = %v, want persisted", receipt.State())
	}
}

// A turn consumes only its own mailbox — input bound to a queued job's
// mailbox stays pending across the other job's runTurn, which is what
// makes exact-turn steering safe against the running job.
func TestRunTurnConsumesOnlyItsOwnMailbox(t *testing.T) {
	conv, d := seedSteerRoom(t, repoconversation.NewConversationRepository(t.TempDir()))
	running := NewSteeringInput()
	queued := NewSteeringInput()
	running.push("for the running turn")
	queuedReceipt := queued.Submit(valueobject.UserInput{Text: "for the queued job"})
	if err := d.runTurn(t.Context(), "room", conv, EnvironmentBundle{}, nil, entity.Model{}, doneSvc{}, nil, running, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if queuedReceipt.State() != SteeringPending {
		t.Fatalf("queued job's receipt = %v, want pending", queuedReceipt.State())
	}
	for _, m := range conv.Messages() {
		if m.Content() == "for the queued job" {
			t.Fatal("the running turn must not consume the queued job's mailbox")
		}
	}
}

// A recall and the turn loop's claim race on one receipt — the
// mailbox's atomic claim boundary admits exactly one winner per input,
// never both.
func TestSteeringInputRecallVsClaimRace(t *testing.T) {
	for i := range 200 {
		in := NewSteeringInput()
		r := in.Submit(valueobject.UserInput{Text: "raced input"})
		start := make(chan struct{})
		var wg sync.WaitGroup
		var recalled bool
		var claimed []steeringEntry
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			recalled = in.Recall(r)
		}()
		go func() {
			defer wg.Done()
			<-start
			claimed = in.claim()
		}()
		close(start)
		wg.Wait()
		// Exactly one outcome per submission: the receipt is either
		// withdrawn before claim or claimed for the fold — never both.
		switch {
		case recalled:
			if len(claimed) != 0 {
				t.Fatalf("iter %d: recall won but claim still took %d entries", i, len(claimed))
			}
			if r.State() != SteeringRecalled {
				t.Fatalf("iter %d: state = %v, want recalled", i, r.State())
			}
		case len(claimed) == 1 && claimed[0].input.Text == "raced input":
			if r.State() != SteeringClaimed {
				t.Fatalf("iter %d: state = %v, want claimed", i, r.State())
			}
		default:
			t.Fatalf("iter %d: no winner — recalled=%v claimed=%d", i, recalled, len(claimed))
		}
	}
}

// Send binds the caller-supplied mailbox end to end: the worker's
// out-of-band Steer path and web Submits share it, while an unbound
// Send falls back to its internal one.
func TestSendSteerUsesSuppliedMailbox(t *testing.T) {
	box := NewSteeringInput()
	j := &Send{Opts: SendOptions{Steering: box}}
	j.Steer("cli input")
	web := box.Submit(valueobject.UserInput{Text: "web input"})
	if j.mailbox() != box {
		t.Fatal("a bound send must fold the supplied mailbox")
	}
	claimed := box.claim()
	if len(claimed) != 2 || claimed[0].input.Text != "cli input" || claimed[1].input.Text != "web input" {
		t.Fatalf("bound mailbox FIFO = %+v", claimed)
	}
	if web.State() != SteeringClaimed {
		t.Fatalf("web receipt = %v, want claimed", web.State())
	}
	plain := &Send{}
	plain.Steer("internal")
	if got, ok := plain.steers.popLast(); !ok || got != "internal" {
		t.Fatalf("unbound send keeps its internal mailbox: %q,%v", got, ok)
	}
}

// A full mailbox refuses the submission and buffers nothing: the caller reports
// the refusal instead of losing the input, and the inputs already waiting keep
// their place.
func TestSteeringMailboxRefusesBeyondItsBound(t *testing.T) {
	s := NewSteeringInput()
	if _, err := s.SubmitBefore(valueobject.UserInput{Text: "first"}, 1, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitBefore(valueobject.UserInput{Text: "second"}, 1, nil); !errors.Is(err, ErrMailboxFull) {
		t.Fatalf("second submit = %v, want a full mailbox", err)
	}
	pending := s.claim()
	if len(pending) != 1 || pending[0].input.Text != "first" {
		t.Fatalf("a refused submit changed the mailbox: %+v", pending)
	}
}

// The before hook runs before the turn is told the input arrived: a record the
// delivery depends on is never written after the text went out.
func TestSteeringBeforeHookRunsBeforeDelivery(t *testing.T) {
	s := NewSteeringInput()
	var order []string
	var mu sync.Mutex
	if _, err := s.SubmitBefore(valueobject.UserInput{Text: "relay"}, 0, func() *entity.Message {
		mu.Lock()
		order = append(order, "record")
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.arrived():
		mu.Lock()
		order = append(order, "delivery")
		mu.Unlock()
	case <-time.After(time.Second):
		t.Fatal("the mailbox never signalled")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "record" || order[1] != "delivery" {
		t.Fatalf("order = %v, want the record before delivery", order)
	}
}

// A rescue that cannot hand the line over says so once, naming the room and the
// record — never the words.
func TestRescueLogsAFailedFollowUp(t *testing.T) {
	var buf strings.Builder
	applied := errors.New("enqueue refused")
	s := NewSteeringInput()
	rec, err := entity.NewUserMessage(valueobject.UserInput{Text: "verdict from PA"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitBefore(valueobject.UserInput{Text: "verdict from PA", Relay: &valueobject.Relay{ID: "c_pa"}}, 0, func() *entity.Message {
		return &rec
	}); err != nil {
		t.Fatal(err)
	}
	j := &Send{Deps: Deps{Log: slog.New(slog.NewTextHandler(&buf, nil)), FollowUp: func(string, entity.Message) error { return applied }}, agentRoom: true}
	j.Opts.Steering = s
	j.Bind("c_log", nil)
	j.rescueSteers()
	out := buf.String()
	if n := strings.Count(out, "relay rescue failed"); n != 1 {
		t.Fatalf("a failed rescue logged %d entries, want one: %s", n, out)
	}
	if !strings.Contains(out, "c_log") || !strings.Contains(out, rec.ID()) || !strings.Contains(out, "enqueue refused") {
		t.Fatalf("the entry does not name the room, the record and the error: %s", out)
	}
	if strings.Contains(out, "verdict from PA") {
		t.Fatalf("the log carries another room's words: %s", out)
	}
}

// A rescue that works logs nothing: this line is for trouble, not for noise.
func TestRescueLogsNothingWhenItSucceeds(t *testing.T) {
	var buf strings.Builder
	s := NewSteeringInput()
	rec, err := entity.NewUserMessage(valueobject.UserInput{Text: "arrived"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitBefore(valueobject.UserInput{Text: "arrived", Relay: &valueobject.Relay{ID: "c_pa"}}, 0, func() *entity.Message {
		return &rec
	}); err != nil {
		t.Fatal(err)
	}
	j := &Send{Deps: Deps{Log: slog.New(slog.NewTextHandler(&buf, nil)), FollowUp: func(string, entity.Message) error { return nil }}, agentRoom: true}
	j.Opts.Steering = s
	j.Bind("c_quiet", nil)
	j.rescueSteers()
	if buf.Len() != 0 {
		t.Fatalf("a working rescue logged: %s", buf.String())
	}
}
