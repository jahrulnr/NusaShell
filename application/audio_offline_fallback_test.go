package application

import (
	"errors"
	"strings"
	"testing"

	"nusashell/domain"
)

// TestOfflineFillsGap pins the zero-config UX: with NO audio fallback
// configured at all, read_media still succeeds via the local engine (when
// available) instead of dead-ending, and discloses route=offline.
func TestOfflineFillsGap(t *testing.T) {
	s := newSttHarness(t)
	s.app.Settings = &fakeSettingsStore{} // AudioProviderID empty
	s.offline.text = "transkrip lokal"

	out, _, err := s.app.executeReadAudio(s.run, s.toolCall, ModelCapabilities{}, s.app.Settings.Get())
	if err != nil {
		t.Fatalf("offline fallback should serve, got error: %v", err)
	}
	if !strings.Contains(out, "transkrip lokal") {
		t.Errorf("expected offline transcript, got %q", out)
	}
	if !strings.Contains(out, "route: offline") {
		t.Errorf("meta should disclose route=offline, got %q", out)
	}
}

// TestCloudBeatsOffline pins explicit-over-implicit: when the user HAS
// configured an stt-kind audio fallback, it is used even if the local
// engine is also available.
func TestCloudBeatsOffline(t *testing.T) {
	s := newSttHarness(t)
	s.app.Settings = &fakeSettingsStore{settings: domain.Settings{AudioProviderID: "p1", AudioModelID: "m"}}
	s.cloud.text = "transkrip awan"
	s.offline.text = "transkrip lokal"

	out, _, err := s.app.executeReadAudio(s.run, s.toolCall, ModelCapabilities{}, s.app.Settings.Get())
	if err != nil {
		t.Fatalf("cloud route should succeed: %v", err)
	}
	if !strings.Contains(out, "transkrip awan") || strings.Contains(out, "transkrip lokal") {
		t.Errorf("cloud transcript should win, got %q", out)
	}
}

// TestCloudFailureDegradesToOffline: a broken/misconfigured cloud provider
// (403 etc.) must not dead-end the tool — fall through to offline and log.
func TestCloudFailureDegradesToOffline(t *testing.T) {
	s := newSttHarness(t)
	s.app.Settings = &fakeSettingsStore{settings: domain.Settings{AudioProviderID: "p1", AudioModelID: "m"}}
	s.cloud.err = errors.New("HTTP 403: forbidden")
	s.offline.text = "transkrip lokal"

	out, _, err := s.app.executeReadAudio(s.run, s.toolCall, ModelCapabilities{}, s.app.Settings.Get())
	if err != nil {
		t.Fatalf("should degrade to offline, got error: %v", err)
	}
	if !strings.Contains(out, "transkrip lokal") {
		t.Errorf("offline should serve after cloud failure, got %q", out)
	}
}

// TestNoRoutesAtAllKeepsOldMessage: neither cloud nor offline available →
// the original helpful message stays.
func TestNoRoutesAtAllKeepsOldMessage(t *testing.T) {
	s := newSttHarness(t)
	s.app.Settings = &fakeSettingsStore{}
	s.offline.available = false
	s.offline.reason = "not built"

	out, _, err := s.app.executeReadAudio(s.run, s.toolCall, ModelCapabilities{}, s.app.Settings.Get())
	if err != nil {
		t.Fatalf("graceful message expected, got Go error: %v", err)
	}
	if !strings.Contains(out, "does not support audio input") {
		t.Errorf("legacy guidance missing, got %q", out)
	}
}
