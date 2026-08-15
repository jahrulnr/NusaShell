package application

import (
	"testing"
)

func TestExtractDecision(t *testing.T) {
	obs := ExtractObservations("let's use postgres for the database", "")
	if len(obs) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(obs))
	}
	if obs[0].Signal != SignalDecision {
		t.Errorf("signal = %q, want decision", obs[0].Signal)
	}
	if obs[0].Content != "Decision: use postgres for the database" {
		t.Errorf("content = %q", obs[0].Content)
	}
}

func TestExtractPreference(t *testing.T) {
	obs := ExtractObservations("I prefer tabs over spaces, and always use gofmt", "")
	if len(obs) < 1 {
		t.Fatalf("expected at least 1 observation, got %d", len(obs))
	}
	found := false
	for _, o := range obs {
		if o.Signal == SignalPreference {
			found = true
		}
	}
	if !found {
		t.Error("no preference signal found")
	}
}

func TestExtractErrorFix(t *testing.T) {
	assistant := "Error: connection refused on port 5432\nFix: check if postgres is running with pg_isready"
	obs := ExtractObservations("", assistant)
	if len(obs) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(obs))
	}
	if obs[0].Signal != SignalError {
		t.Errorf("signal = %q, want error", obs[0].Signal)
	}
}

func TestExtractFact(t *testing.T) {
	obs := ExtractObservations("remember that the API rate limit is 100 requests per minute", "")
	if len(obs) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(obs))
	}
	if obs[0].Signal != SignalFact {
		t.Errorf("signal = %q, want fact", obs[0].Signal)
	}
	if obs[0].Weight != 0.8 {
		t.Errorf("weight = %v, want 0.8", obs[0].Weight)
	}
}

func TestExtractNothing(t *testing.T) {
	obs := ExtractObservations("hello world", "hi there")
	if len(obs) != 0 {
		t.Fatalf("expected 0 observations, got %d: %+v", len(obs), obs)
	}
}

func TestExtractMultipleSignals(t *testing.T) {
	user := "let's use docker. I prefer alpine images. remember that the build context is ./src"
	obs := ExtractObservations(user, "")
	if len(obs) < 3 {
		t.Fatalf("expected at least 3 observations, got %d", len(obs))
	}
	signals := map[LearningSignal]bool{}
	for _, o := range obs {
		signals[o.Signal] = true
	}
	if !signals[SignalDecision] || !signals[SignalPreference] || !signals[SignalFact] {
		t.Errorf("missing signals, got: %+v", signals)
	}
}

func TestObservationToMemory(t *testing.T) {
	obs := ExtractedObservation{
		Signal:  SignalFact,
		Content: "test fact",
		Source:  "user",
		Weight:  0.8,
		Tags:    []string{"fact"},
	}
	m := ObservationToMemory(obs)
	if m.Content != "test fact" {
		t.Errorf("content = %q", m.Content)
	}
	if m.Source != "user" {
		t.Errorf("source = %q", m.Source)
	}
	if len(m.Tags) != 1 || m.Tags[0] != "fact" {
		t.Errorf("tags = %v", m.Tags)
	}
}
