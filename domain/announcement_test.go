package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAutoContinueAnnouncementArgs(t *testing.T) {
	raw := AutoContinueAnnouncementArgs(3, 5)
	var parsed struct {
		Type          string `json:"type"`
		ContinuesUsed int    `json:"continues_used"`
		OpenTodos     int    `json:"open_todos"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("args must be valid JSON: %v (%s)", err, raw)
	}
	if parsed.Type != "auto_continue" || parsed.ContinuesUsed != 3 || parsed.OpenTodos != 5 {
		t.Fatalf("args must self-describe the chain state: %s", raw)
	}
	if !strings.Contains(raw, `"continues_used":3`) || !strings.Contains(raw, `"open_todos":5`) {
		t.Fatalf("args must carry state fields: %s", raw)
	}
}

func TestIsAnnouncementCallID(t *testing.T) {
	if !IsAnnouncementCallID("announce-abc123") {
		t.Error("announce- prefixed id must match")
	}
	for _, id := range []string{"call_123", "announce", "announc-", ""} {
		if IsAnnouncementCallID(id) {
			t.Errorf("id %q must not match", id)
		}
	}
}
