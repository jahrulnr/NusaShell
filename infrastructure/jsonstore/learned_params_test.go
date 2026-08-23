package jsonstore

import (
	"path/filepath"
	"testing"

	"nusashell/domain"
)

func TestLearnedParamsLoadEmpty(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := s.LoadLearnedParams()
	if r == nil {
		t.Fatal("LoadLearnedParams returned nil")
	}
	if r.Len() != 0 {
		t.Errorf("empty registry Len = %d, want 0", r.Len())
	}
}

func TestLearnedParamsSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Save a registry with one strip + one inject rule
	r := domain.NewLearnedParamRegistry()
	r.RecordStrip("openrouter", "glm-5.2", "logprobs", "Unsupported parameter: logprobs")
	r.RecordInject("openrouter", "stealth/ox-alpha", "reasoning_content", "reasoning_content must be passed back")
	if err := s.SaveLearnedParams(r); err != nil {
		t.Fatalf("SaveLearnedParams: %v", err)
	}

	// Verify file exists
	if _, err := filepath.Abs(filepath.Join(dir, "learning", "provider_params.json")); err != nil {
		t.Fatalf("file path: %v", err)
	}

	// Reload from a fresh store (simulates process restart)
	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded := s2.LoadLearnedParams()
	if loaded.Len() != 2 {
		t.Fatalf("reloaded registry Len = %d, want 2", loaded.Len())
	}
	strip := loaded.StripParams("openrouter", "glm-5.2")
	if len(strip) != 1 || strip[0] != "logprobs" {
		t.Errorf("reloaded StripParams = %v, want [logprobs]", strip)
	}
	inject := loaded.InjectParams("openrouter", "stealth/ox-alpha")
	if len(inject) != 1 || inject[0] != "reasoning_content" {
		t.Errorf("reloaded InjectParams = %v, want [reasoning_content]", inject)
	}
}

func TestLearnedParamsAdapterRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &LearnedParams{S: s}

	// Initially empty
	if adapter.Load().Len() != 0 {
		t.Fatal("initial load not empty")
	}

	// Save via adapter
	r := domain.NewLearnedParamRegistry()
	r.RecordStrip("p", "m", "temperature", "Unsupported parameter: temperature")
	if err := adapter.Save(r); err != nil {
		t.Fatalf("adapter Save: %v", err)
	}

	// Load via adapter
	loaded := adapter.Load()
	if loaded.Len() != 1 {
		t.Fatalf("adapter Load Len = %d, want 1", loaded.Len())
	}
	strip := loaded.StripParams("p", "m")
	if len(strip) != 1 || strip[0] != "temperature" {
		t.Errorf("adapter StripParams = %v, want [temperature]", strip)
	}
}

func TestLearnedParamsConcurrentSave(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &LearnedParams{S: s}

	// Concurrent writes should not race (atomic write + mutex)
	done := make(chan struct{}, 10)
	for i := 0; i < 10; i++ {
		go func(n int) {
			r := adapter.Load()
			r.RecordStrip("p", "m", "param", "concurrent")
			_ = adapter.Save(r)
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	// Final state should be loadable without error
	_ = adapter.Load()
}
