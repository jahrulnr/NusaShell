package appconversation

import (
	"log/slog"
	"strings"
	"testing"

	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	reporecording "github.com/jahrulnr/nusashell/infrastructure/repository/recording"
)

// A recording resolves only in its own thread; a live one carries a meta
// brief, a stopped one its summary.
func TestRecorded(t *testing.T) {
	store := reporecording.NewStore(t.TempDir(), slog.Default())
	defer store.Shutdown()
	app := &ConversationApp{}
	app.UseRecordings(store)

	w, err := store.Start("s1", "rec-room", "default", "local", func(s string) string { return s })
	if err != nil {
		t.Fatal(err)
	}
	meta := w.Meta()

	if _, err := app.Recorded(t.Context(), "other-room", meta.ID); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("foreign recording err = %v, want 400", err)
	}
	if _, err := app.Recorded(t.Context(), "rec-room", "rec_nope"); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("unknown recording err = %v, want 400", err)
	}

	// Live: the brief stands in for the summary it writes at Stop.
	ref, err := app.Recorded(t.Context(), "rec-room", meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Status != "recording" || !strings.Contains(ref.Summary, "browser_get_timeline") || !strings.Contains(ref.Summary, "status recording") {
		t.Fatalf("live ref = %+v", ref)
	}

	stopped, err := w.Stop()
	if err != nil {
		t.Fatal(err)
	}
	ref, err = app.Recorded(t.Context(), "rec-room", stopped.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Status != "completed" || strings.TrimSpace(ref.Summary) == "" {
		t.Fatalf("stopped ref = %+v", ref)
	}
}
