package appnotify

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// fakeSender records what would have been shown.
type fakeSender struct {
	mu    sync.Mutex
	avail bool
	got   []dservice.Notification
}

func (f *fakeSender) Name() string    { return "fake" }
func (f *fakeSender) Available() bool { return f.avail }
func (f *fakeSender) Reason() string  { return "nowhere" }
func (f *fakeSender) Notify(_ context.Context, n dservice.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, n)
	return nil
}

func (f *fakeSender) take() []dservice.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]dservice.Notification(nil), f.got...)
}

type fakeRooms struct{ titles map[string]string }

func (r fakeRooms) Info(_ context.Context, id string) (drepo.ConversationInfo, error) {
	if t, ok := r.titles[id]; ok {
		return drepo.ConversationInfo{ID: id, Title: t}, nil
	}
	return drepo.ConversationInfo{}, apperror.NewNotFoundError("conversation "+id, nil)
}

func setup(t *testing.T, enabled bool) (*App, *fakeSender) {
	t.Helper()
	s := &fakeSender{avail: true}
	return New(s, func() bool { return enabled }, fakeRooms{titles: map[string]string{"c_1": "Refactor the hub"}}, "NusaShell", nil), s
}

// waitFor polls until count notifications were recorded or the deadline.
func waitFor(t *testing.T, s *fakeSender, count int) []dservice.Notification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := s.take(); len(got) >= count {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	return s.take()
}

func approvalEvent(t *testing.T) event.TurnEvent {
	t.Helper()
	req, err := valueobject.NewApprovalRequest("apr_1", "call_1", "Run go test", "", enum.ToolActionExecute,
		[]valueobject.ApprovalOption{{ID: "yes", Name: "Allow", Choice: enum.ApprovalAllowOnce}})
	if err != nil {
		t.Fatal(err)
	}
	return event.NewApprovalEvent(req)
}

// A machine turn the owner never asked for stays silent: a wake raised so
// a room reads its completions, or an automation firing on schedule, is not
// a reply. Removing the origin filter turns this test red — the noise is
// exactly what makes an owner disable notifications entirely.
func TestMachineTurnsStaySilent(t *testing.T) {
	app, s := setup(t, true)
	app.TurnFinished("c_1", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginCompletion}, false, nil)
	app.TurnFinished("c_1", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginAutomation, Name: "nightly"}, false, nil)
	if got := waitFor(t, s, 1); len(got) != 0 {
		t.Fatalf("machine turns must not notify: %+v", got)
	}
}

// A relay turn — one thread answering another — still notifies: the owner
// did not type it, but it is a reply in his rooms, not machine housekeeping.
func TestConversationTurnStillNotifies(t *testing.T) {
	app, s := setup(t, true)
	app.TurnFinished("c_1", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginConversation}, false, nil)
	if got := waitFor(t, s, 1); len(got) != 1 {
		t.Fatalf("a relay turn's reply should notify: %+v", got)
	}
}

func TestWebTurnEndNotifiesWithRoomTitle(t *testing.T) {
	app, s := setup(t, true)
	app.TurnFinished("c_1", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginWeb}, false, nil)
	got := waitFor(t, s, 1)
	if len(got) != 1 || got[0].Title != "Reply ready" || got[0].Body != "Refactor the hub" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Urgency != dservice.UrgencyNormal {
		t.Fatalf("a finished turn is plain info: %+v", got[0])
	}
}

func TestExternalAndCancelledTurnsStaySilent(t *testing.T) {
	app, s := setup(t, true)
	// Chat and automation turns report through their own paths.
	app.TurnFinished("c_1", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginWeb}, true, nil)
	// The owner's own stop is no news.
	app.TurnFinished("c_1", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginWeb}, false, apperror.New(408, "turn canceled", nil))
	if got := waitFor(t, s, 1); len(got) != 0 {
		t.Fatalf("external or cancelled turns must not notify: %+v", got)
	}
}

func TestFailedTurnIsNormalUrgencyError(t *testing.T) {
	app, s := setup(t, true)
	app.TurnFinished("c_1", "edit", valueobject.TurnOrigin{Kind: enum.TurnOriginWeb}, false, apperror.New(502, "upstream is down", nil))
	got := waitFor(t, s, 1)
	if len(got) != 1 || got[0].Title != "Turn failed" {
		t.Fatalf("got %+v", got)
	}
	// Failures come and go; only a question waits on the owner, so a
	// failure is normal urgency — never sticky.
	if got[0].Urgency != dservice.UrgencyNormal {
		t.Fatalf("a failed turn must not be sticky: %+v", got[0])
	}
}

func TestApprovalIsCriticalQuestion(t *testing.T) {
	app, s := setup(t, true)
	app.TurnEvent("c_1", approvalEvent(t))
	got := waitFor(t, s, 1)
	if len(got) != 1 || got[0].Title != "Approval needed" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Urgency != dservice.UrgencyCritical {
		t.Fatalf("an approval must stay up until answered: %+v", got[0])
	}
}

func TestAutomationOutcomes(t *testing.T) {
	app, s := setup(t, true)
	app.AutomationRan("Nightly", enum.RunCompleted, "")
	app.AutomationRan("Nightly", enum.RunFailed, "the agent died")
	app.AutomationRan("Nightly", enum.RunInterrupted, "server stopped")
	got := waitFor(t, s, 2)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	byTitle := map[string]dservice.Notification{}
	for _, n := range got {
		byTitle[n.Title] = n
	}
	finished, failed := byTitle["Automation finished"], byTitle["Automation failed"]
	if finished.Urgency != dservice.UrgencyNormal {
		t.Fatalf("completed = %+v", finished)
	}
	if failed.Urgency != dservice.UrgencyNormal {
		t.Fatalf("a failing schedule must not stack sticky notifications: %+v", failed)
	}
}

func TestMutedDisabledAndUnavailableSendNothing(t *testing.T) {
	// The owner's switch off.
	app, s := setup(t, false)
	app.TurnFinished("c_1", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginWeb}, false, nil)
	// Muted for shutdown.
	app2, _ := setup(t, true)
	app2.Mute()
	app2.TurnFinished("c_1", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginWeb}, false, nil)
	// No usable backend.
	app3, s3 := setup(t, true)
	s3.avail = false
	app3.TurnFinished("c_1", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginWeb}, false, nil)
	if got := waitFor(t, s, 1); len(got) != 0 {
		t.Fatalf("disabled must be silent: %+v", got)
	}
}

func TestUpdateAvailableNotifies(t *testing.T) {
	app, s := setup(t, true)
	app.UpdateAvailable("1.2.3")
	got := waitFor(t, s, 1)
	if len(got) != 1 || got[0].Title != "Update available" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Body == "" {
		t.Fatal("the body names the version and where to install")
	}
	if got[0].Icon != "system-software-update" || got[0].Urgency != dservice.UrgencyLow {
		t.Fatalf("update notification icon/urgency = %q/%q", got[0].Icon, got[0].Urgency)
	}
}
