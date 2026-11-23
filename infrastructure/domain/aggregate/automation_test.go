package aggregate

import (
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

func TestAutomationCronClaimSkipsMissedRuns(t *testing.T) {
	now := time.Date(2026, 11, 22, 8, 30, 0, 0, time.UTC)
	a, err := NewAutomation("", "Daily", "Summarize", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetRuntime("claude", "", ""); err != nil {
		t.Fatal(err)
	}
	s, err := valueobject.NewCronSchedule("0 9 * * *", "Asia/Jakarta")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetSchedule(s, now); err != nil {
		t.Fatal(err)
	}
	// 09:00 Jakarta is 02:00 UTC; the next one after 08:30 UTC is tomorrow.
	want := time.Date(2026, 11, 23, 2, 0, 0, 0, time.UTC)
	if !a.NextRunAt().Equal(want) {
		t.Fatalf("next = %v, want %v", a.NextRunAt().UTC(), want)
	}
	late := want.Add(72 * time.Hour) // the server was down three days
	if !a.Due(late) {
		t.Fatal("a missed run is due")
	}
	if got := a.Claim(late); !got.Equal(want) {
		t.Fatalf("claimed %v", got)
	}
	if !a.NextRunAt().After(late) || a.Due(late) {
		t.Fatalf("missed runs must collapse into one; next = %v", a.NextRunAt())
	}
}

func TestAutomationOnceAndEdits(t *testing.T) {
	now := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)
	a, _ := NewAutomation("", "Remind", "Ping", now)
	_ = a.SetRuntime("", "openrouter", "openrouter/auto")
	past, _ := valueobject.NewOnceSchedule(now.Add(-time.Hour), "UTC")
	if err := a.SetSchedule(past, now); err == nil {
		t.Fatal("a one-time run in the past must be refused")
	}
	at := now.Add(time.Hour)
	once, _ := valueobject.NewOnceSchedule(at, "UTC")
	if err := a.SetSchedule(once, now); err != nil {
		t.Fatal(err)
	}
	a.Claim(at)
	if !a.NextRunAt().IsZero() {
		t.Fatal("a one-time run fires once")
	}
	// Editing other fields later keeps the spent schedule valid.
	if err := a.SetSchedule(once, at.Add(24*time.Hour)); err != nil {
		t.Fatalf("an unchanged spent schedule must stay: %v", err)
	}
	a.SetThread(enum.AutomationThreadSame)
	a.KeepConversation("c_1")
	if err := a.SetRuntime("claude", "", ""); err != nil || a.ConversationID() != "" {
		t.Fatalf("a new runtime starts a new kept thread: %v %q", err, a.ConversationID())
	}
	if err := a.SetRuntime("claude", "openrouter", "x"); err == nil {
		t.Fatal("agent and provider together must be refused")
	}
	if err := a.SetExpiry(now.Add(30*time.Minute), now); err != nil {
		t.Fatal(err)
	}
	if !a.Expired(now.Add(time.Hour)) {
		t.Fatal("expiry")
	}
}

func TestAutomationSetExpiry(t *testing.T) {
	now := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)
	a, _ := NewAutomation("", "Hourly", "Check", now)
	s, _ := valueobject.NewCronSchedule("0 * * * *", "UTC")
	_ = a.SetSchedule(s, now) // next = 09:00

	// A non-zero expiry that is not after now is refused.
	if err := a.SetExpiry(now, now); err == nil {
		t.Fatal("an expiry already passed must be refused")
	}
	if err := a.SetExpiry(now.Add(-time.Hour), now); err == nil {
		t.Fatal("a past expiry must be refused")
	}
	if !a.ExpiresAt().IsZero() {
		t.Fatal("a refused expiry must not stick")
	}

	// Expire the automation at 09:30 and look at it at 10:00: its stored
	// next run (09:00) is stale and would catch up the moment the expiry
	// lifted. Extending the expiry re-plans from now instead.
	if err := a.SetExpiry(now.Add(90*time.Minute), now); err != nil {
		t.Fatal(err)
	}
	later := now.Add(2 * time.Hour)
	if !a.Expired(later) {
		t.Fatal("the automation should be expired")
	}
	if err := a.SetExpiry(later.Add(24*time.Hour), later); err != nil {
		t.Fatal(err)
	}
	if want := later.Add(time.Hour); !a.NextRunAt().Equal(want) {
		t.Fatalf("re-planned next = %v, want %v", a.NextRunAt(), want)
	}

	// Clearing the expiry of an expired automation re-plans too.
	beyond := later.Add(25 * time.Hour)
	if !a.Expired(beyond) {
		t.Fatal("the automation should be expired again")
	}
	// Keeping the passed expiry unchanged is not a new expiry: other
	// fields of an expired automation stay editable.
	if err := a.SetExpiry(a.ExpiresAt(), beyond); err != nil {
		t.Fatalf("an unchanged passed expiry must be kept: %v", err)
	}
	if err := a.SetExpiry(time.Time{}, beyond); err != nil {
		t.Fatal(err)
	}
	if want := beyond.Add(time.Hour); !a.NextRunAt().Equal(want) {
		t.Fatalf("re-planned next = %v, want %v", a.NextRunAt(), want)
	}
}

func TestAutomationResumePlansFromNow(t *testing.T) {
	now := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)
	a, _ := NewAutomation("", "Hourly", "Check", now)
	_ = a.SetRuntime("claude", "", "")
	s, _ := valueobject.NewCronSchedule("0 * * * *", "UTC")
	_ = a.SetSchedule(s, now)
	a.SetEnabled(false, now)
	later := now.Add(5*time.Hour + 30*time.Minute)
	a.SetEnabled(true, later)
	if want := time.Date(2026, 11, 22, 14, 0, 0, 0, time.UTC); !a.NextRunAt().Equal(want) {
		t.Fatalf("resumed next = %v, want %v", a.NextRunAt(), want)
	}
}
