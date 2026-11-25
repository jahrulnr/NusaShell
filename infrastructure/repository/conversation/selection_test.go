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

// An agent room's own picks come back after a reload: the agent bind is
// part of the room's identity, not a change of agent — hydration binding
// ""→the-stored-agent must not trigger the clearing a real switch owns.
// Before the fix, Load restored provider/model/effort/upstream first and
// bound the agent after, so the bind's deliberate clearing wiped exactly
// the picks the room was saved with — and the next Save wrote the wipe.
func TestAgentRoomPicksSurviveReload(t *testing.T) {
	dir := t.TempDir()
	repo := NewConversationRepository(dir)
	conv := newConv(t, "room")
	if err := conv.BindAgent("devin"); err != nil {
		t.Fatal(err)
	}
	conv.SetModelID("claude-haiku-5-5-medium")
	conv.SetProviderID("anthropic")
	conv.SetEffort(enum.EffortXHigh)
	conv.SetUpstream("Alpha")
	conv.SetAgentSession("sess-9")
	if err := conv.SetAgentOption("model", "claude-haiku-5-5-medium"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}

	again := NewConversationRepository(dir) // a fresh process
	loaded, err := again.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ModelID() != "claude-haiku-5-5-medium" {
		t.Fatalf("model pick died in hydration: %q", loaded.ModelID())
	}
	if loaded.ProviderID() != "anthropic" || loaded.Effort() != enum.EffortXHigh || loaded.Upstream() != "Alpha" {
		t.Fatalf("provider/effort/upstream died in hydration: %q %q %q", loaded.ProviderID(), loaded.Effort(), loaded.Upstream())
	}
	if loaded.AgentSession() != "sess-9" {
		t.Fatalf("agent session = %q, want sess-9", loaded.AgentSession())
	}
	picks := loaded.AgentOptions()
	if len(picks) != 1 || picks[0].Value != "claude-haiku-5-5-medium" {
		t.Fatalf("agent options = %+v, want the model pick", picks)
	}

	// And the second generation does not write the wipe back: a save of the
	// hydrated room keeps the picks on disk.
	if err := again.Save(t.Context(), loaded); err != nil {
		t.Fatal(err)
	}
	third, err := NewConversationRepository(dir).Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	if third.ModelID() != "claude-haiku-5-5-medium" {
		t.Fatalf("the reload-save-reload generation lost the pick: %q", third.ModelID())
	}
}
