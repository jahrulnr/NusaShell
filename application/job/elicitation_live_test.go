package job

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoagent "github.com/jahrulnr/nusashell/infrastructure/repository/agent"
	repoconversation "github.com/jahrulnr/nusashell/infrastructure/repository/conversation"
	appserviceagent "github.com/jahrulnr/nusashell/infrastructure/service/agent"
)

// Live proof for the owner-answer contract: the real fake ACP binary over
// real stdio, the real broker, and the exact wiring runAgentTurn installs —
// so each evidence line is the wire the agent saw plus the transcript part
// the room recorded.

var (
	liveFakeOnce sync.Once
	liveFakePath string
	liveFakeErr  error
)

func liveFake(t *testing.T) string {
	t.Helper()
	liveFakeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "live-fake-acp-")
		if err != nil {
			liveFakeErr = err
			return
		}
		name := "fake-acp"
		if goruntime.GOOS == "windows" {
			name += ".exe"
		}
		liveFakePath = filepath.Join(dir, name)
		repo, err := filepath.Abs("../..")
		if err != nil {
			liveFakeErr = err
			return
		}
		cmd := exec.Command("go", "build", "-o", liveFakePath, "./cmd/fake-acp-agent")
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Logf("%s", out)
			liveFakeErr = err
		}
	})
	if liveFakeErr != nil {
		t.Fatal(liveFakeErr)
	}
	return liveFakePath
}

// liveElicitDeps wires the turn exactly as production does — repo agents,
// repo conversations, the ACP runtime — with the answer deadline dialed
// down so an expiry lands inside a test.
func liveElicitDeps(t *testing.T, timeout time.Duration) (Deps, *aggregate.Conversation, *Elicitations) {
	t.Helper()
	a, err := aggregate.NewAgent("fake", "Fake", liveFake(t), nil, enum.ApprovalPolicyAsk)
	if err != nil {
		t.Fatal(err)
	}
	agents := repoagent.New(t.TempDir(), nil)
	if err := agents.Save(t.Context(), &a); err != nil {
		t.Fatal(err)
	}
	conv, err := aggregate.NewConversation("live")
	if err != nil {
		t.Fatal(err)
	}
	if err := conv.BindAgent(a.ID()); err != nil {
		t.Fatal(err)
	}
	conv.SetWorkspace(t.TempDir())
	convs := repoconversation.NewConversationRepository(t.TempDir())
	if err := convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	rt := appserviceagent.New(appserviceagent.Options{ClientName: "nusashell-live", ClientVersion: "0"})
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	broker := NewElicitations(timeout)
	return Deps{Agents: agents, AgentRuntime: rt, Convs: convs, Elicitations: broker}, conv, broker
}

// elicitPart digs the recorded elicitation part out of the agent's reply —
// the transcript line that must name both the wire action and who resolved it.
func elicitPart(t *testing.T, conv *aggregate.Conversation) string {
	t.Helper()
	for _, p := range conv.Last().Parts() {
		if p.Kind == entity.MessagePartElicitation {
			return p.Text
		}
	}
	t.Fatalf("no elicitation part recorded: %+v", conv.Last().Parts())
	return ""
}

func livePrompt(t *testing.T, text string) entity.Message {
	t.Helper()
	m, err := entity.NewMessage(enum.RoleUser, text)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// (a) An owner accept with form content reaches the agent verbatim; the
// transcript records accept/owner.
func TestElicitationLiveOwnerAccept(t *testing.T) {
	d, conv, broker := liveElicitDeps(t, 15*time.Minute)
	asked := make(chan valueobject.Elicitation, 1)
	var resolved []string
	emit := func(ev event.TurnEvent) {
		switch ev.Kind() {
		case enum.TurnEventElicitation:
			asked <- ev.Elicitation()
		case enum.TurnEventElicitationResolved:
			resolved = append(resolved, ev.Delta())
		}
	}
	done := make(chan error, 1)
	go func() {
		done <- d.runAgentTurn(t.Context(), conv.ID(), conv, nil, livePrompt(t, "elicit-demo"), emit, nil, valueobject.TurnOrigin{Kind: enum.TurnOriginWeb}, "")
	}()
	e := <-asked
	t.Logf("wire request: id=%s message=%q deadline=%s", e.ID(), e.Message(), e.Deadline().Format("15:04:05.000"))
	if p := broker.Pending(conv.ID()); len(p) != 1 || p[0].Deadline().IsZero() {
		t.Fatalf("pending must carry the deadline: %+v", p)
	}
	if err := broker.Answer(conv.ID(), e.ID(), valueobject.ElicitationAnswer{Action: "accept", Content: []byte(`{"environment":"staging","replicas":2}`)}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	reply := conv.Last().Content()
	t.Logf("agent saw: %s", reply)
	t.Logf("transcript part: %s", elicitPart(t, conv))
	if !strings.Contains(reply, `"action":"accept"`) || !strings.Contains(reply, `"environment":"staging"`) {
		t.Fatalf("the owner answer did not reach the agent: %s", reply)
	}
	part := elicitPart(t, conv)
	if !strings.Contains(part, `"action":"accept"`) || !strings.Contains(part, `"resolvedBy":"owner"`) {
		t.Fatalf("part = %s, want accept/owner", part)
	}
	if len(resolved) != 1 || resolved[0] != "accept" {
		t.Fatalf("resolved event = %v", resolved)
	}
}

// (b) An owner decline reaches the agent and records decline/owner.
func TestElicitationLiveOwnerDecline(t *testing.T) {
	d, conv, broker := liveElicitDeps(t, 15*time.Minute)
	asked := make(chan valueobject.Elicitation, 1)
	emit := func(ev event.TurnEvent) {
		if ev.Kind() == enum.TurnEventElicitation {
			asked <- ev.Elicitation()
		}
	}
	done := make(chan error, 1)
	go func() {
		done <- d.runAgentTurn(t.Context(), conv.ID(), conv, nil, livePrompt(t, "elicit-demo"), emit, nil, valueobject.TurnOrigin{Kind: enum.TurnOriginWeb}, "")
	}()
	e := <-asked
	if err := broker.Answer(conv.ID(), e.ID(), valueobject.ElicitationAnswer{Action: "decline"}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	reply := conv.Last().Content()
	t.Logf("agent saw: %s", reply)
	t.Logf("transcript part: %s", elicitPart(t, conv))
	part := elicitPart(t, conv)
	if !strings.Contains(part, `"action":"decline"`) || !strings.Contains(part, `"resolvedBy":"owner"`) {
		t.Fatalf("part = %s, want decline/owner", part)
	}
}

// (c) The deadline lapsing resolves the request as cancel recorded expired —
// the agent is never left holding an open call, and the transcript does not
// read like a refusal.
func TestElicitationLiveDeadlineExpires(t *testing.T) {
	d, conv, _ := liveElicitDeps(t, 250*time.Millisecond)
	var actions []string
	emit := func(ev event.TurnEvent) {
		if ev.Kind() == enum.TurnEventElicitationResolved {
			actions = append(actions, ev.Delta())
		}
	}
	if err := d.runAgentTurn(t.Context(), conv.ID(), conv, nil, livePrompt(t, "elicit-demo"), emit, nil, valueobject.TurnOrigin{Kind: enum.TurnOriginWeb}, ""); err != nil {
		t.Fatal(err)
	}
	reply := conv.Last().Content()
	t.Logf("agent saw: %s", reply)
	t.Logf("transcript part: %s", elicitPart(t, conv))
	if !strings.Contains(reply, `"action":"cancel"`) {
		t.Fatalf("the agent was left hanging: %s", reply)
	}
	part := elicitPart(t, conv)
	if !strings.Contains(part, `"action":"cancel"`) || !strings.Contains(part, `"resolvedBy":"expired"`) {
		t.Fatalf("part = %s, want cancel/expired", part)
	}
	if len(actions) != 1 || actions[0] != "expired" {
		t.Fatalf("resolved event = %v", actions)
	}
}

// (d) An automation run has no answer surface — the request declines at
// once instead of parking behind a deadline nobody watches.
func TestElicitationLiveAutomationDeclinesAtOnce(t *testing.T) {
	d, conv, broker := liveElicitDeps(t, 15*time.Minute)
	start := time.Now()
	if err := d.runAgentTurn(t.Context(), conv.ID(), conv, nil, livePrompt(t, "elicit-demo"), nil, nil, valueobject.TurnOrigin{Kind: enum.TurnOriginAutomation}, ""); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el > 10*time.Second {
		t.Fatalf("an unattended request waited %s", el)
	}
	reply := conv.Last().Content()
	t.Logf("agent saw: %s", reply)
	t.Logf("transcript part: %s", elicitPart(t, conv))
	if !strings.Contains(reply, `"action":"decline"`) {
		t.Fatalf("the request was not declined to the agent: %s", reply)
	}
	part := elicitPart(t, conv)
	if !strings.Contains(part, `"resolvedBy":"unattended"`) {
		t.Fatalf("part = %s, want decline/unattended", part)
	}
	if len(broker.Pending(conv.ID())) != 0 {
		t.Fatal("a request parked on a run nobody watches")
	}
}

// (e) Cancelling the turn while a request is open cancels it for the agent
// and records unanswered — never owner.
func TestElicitationLiveTurnCancelUnanswered(t *testing.T) {
	d, conv, _ := liveElicitDeps(t, 15*time.Minute)
	ctx, cancel := context.WithCancel(t.Context())
	asked := make(chan valueobject.Elicitation, 1)
	emit := func(ev event.TurnEvent) {
		if ev.Kind() == enum.TurnEventElicitation {
			asked <- ev.Elicitation()
		}
	}
	done := make(chan error, 1)
	go func() {
		done <- d.runAgentTurn(ctx, conv.ID(), conv, nil, livePrompt(t, "elicit-demo"), emit, nil, valueobject.TurnOrigin{Kind: enum.TurnOriginWeb}, "")
	}()
	<-asked
	cancel()
	<-done
	t.Logf("transcript part: %s", elicitPart(t, conv))
	part := elicitPart(t, conv)
	if !strings.Contains(part, `"action":"cancel"`) || !strings.Contains(part, `"resolvedBy":"unanswered"`) {
		t.Fatalf("part = %s, want cancel/unanswered", part)
	}
}

// A url request arrives even though caps promise only form — the surface
// still renders what lands instead of dropping it.
func TestElicitationLiveURLStillPresented(t *testing.T) {
	d, conv, broker := liveElicitDeps(t, 15*time.Minute)
	asked := make(chan valueobject.Elicitation, 1)
	emit := func(ev event.TurnEvent) {
		if ev.Kind() == enum.TurnEventElicitation {
			asked <- ev.Elicitation()
		}
	}
	done := make(chan error, 1)
	go func() {
		done <- d.runAgentTurn(t.Context(), conv.ID(), conv, nil, livePrompt(t, "elicit-demo url"), emit, nil, valueobject.TurnOrigin{Kind: enum.TurnOriginWeb}, "")
	}()
	e := <-asked
	t.Logf("wire request: mode=%s url=%s message=%q", e.Mode(), e.URL(), e.Message())
	if e.Mode() != "url" || e.URL() == "" {
		t.Fatalf("a url request lost its payload: mode=%q url=%q", e.Mode(), e.URL())
	}
	if p := broker.Pending(conv.ID()); len(p) != 1 || p[0].URL() != e.URL() {
		t.Fatalf("pending lost the url: %+v", p)
	}
	if err := broker.Answer(conv.ID(), e.ID(), valueobject.ElicitationAnswer{Action: "decline"}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	t.Logf("transcript part: %s", elicitPart(t, conv))
}

// (f) The first resolution stands: an answer for a settled request is a 404.
func TestElicitationLiveAnswerAfterResolve404(t *testing.T) {
	d, conv, broker := liveElicitDeps(t, 15*time.Minute)
	asked := make(chan valueobject.Elicitation, 1)
	emit := func(ev event.TurnEvent) {
		if ev.Kind() == enum.TurnEventElicitation {
			asked <- ev.Elicitation()
		}
	}
	done := make(chan error, 1)
	go func() {
		done <- d.runAgentTurn(t.Context(), conv.ID(), conv, nil, livePrompt(t, "elicit-demo"), emit, nil, valueobject.TurnOrigin{Kind: enum.TurnOriginWeb}, "")
	}()
	e := <-asked
	if err := broker.Answer(conv.ID(), e.ID(), valueobject.ElicitationAnswer{Action: "accept", Content: []byte(`{"environment":"staging","replicas":2}`)}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	err := broker.Answer(conv.ID(), e.ID(), valueobject.ElicitationAnswer{Action: "decline"})
	if apperror.As(err).StatusCode() != 404 {
		t.Fatalf("an answer after resolution = %v, want 404", err)
	}
	t.Logf("late answer refused with %v", err)
}
