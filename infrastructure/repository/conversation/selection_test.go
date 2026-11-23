package repoconversation

import (
	"os"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// The effort and upstream pin the owner picked for a room come back after a
// restart: they are written to the room's record, read by Load, and served
// by Info for the picker.
func TestEffortAndUpstreamSurviveReload(t *testing.T) {
	dir := t.TempDir()
	repo := NewConversationRepository(dir)
	conv := newConv(t, "room")
	conv.SetEffort(enum.EffortXHigh)
	conv.SetUpstream("Alpha")
	if err := repo.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}

	again := NewConversationRepository(dir) // a fresh process
	loaded, err := again.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Effort() != enum.EffortXHigh || loaded.Upstream() != "Alpha" {
		t.Fatalf("loaded = %q, %q", loaded.Effort(), loaded.Upstream())
	}
	info, err := again.Info(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	if info.Effort != "xhigh" || info.Upstream != "Alpha" {
		t.Fatalf("info = %q, %q", info.Effort, info.Upstream)
	}

	// Back to the model's own: nothing is written.
	loaded.SetEffort(enum.EffortDefault)
	loaded.SetUpstream("")
	if err := again.Save(t.Context(), loaded); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(again.convDir("room") + "/meta.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "effort") || strings.Contains(string(raw), "upstream") {
		t.Fatalf("a room on the model's own effort wrote it anyway: %s", raw)
	}
}

// A record naming an effort this build does not know is corrupt, and says so
// rather than quietly dropping what the owner set.
func TestLoadRefusesAnUnknownEffort(t *testing.T) {
	repo := NewConversationRepository(t.TempDir())
	if err := repo.Save(t.Context(), newConv(t, "room")); err != nil {
		t.Fatal(err)
	}
	path := repo.convDir("room") + "/meta.json"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"environment"`, `"effort":"furious","environment"`, 1))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Load(t.Context(), "room"); err == nil {
		t.Fatal("Load must refuse an unknown effort")
	}
}
