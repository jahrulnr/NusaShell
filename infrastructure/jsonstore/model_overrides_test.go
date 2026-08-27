package jsonstore

import (
	"testing"

	"nusashell/domain"
)

func boolPtrT(b bool) *bool { return &b }
func intPtrT(i int) *int    { return &i }

func TestModelOverridesLoadEmpty(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := s.LoadModelOverrides()
	if r == nil {
		t.Fatal("LoadModelOverrides returned nil")
	}
	if r.Len() != 0 {
		t.Errorf("empty registry Len = %d, want 0", r.Len())
	}
}

func TestModelOverridesSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	r := domain.NewModelOverrideRegistry()
	if err := r.Set(&domain.ModelOverride{
		Provider: "tokenrouter", Model: "deepseek-v4-flash",
		Vision: boolPtrT(false), Context: intPtrT(1000000),
		Source: "review-agent", Reason: "catalog says 200k but provider serves 1M",
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.SaveModelOverrides(r); err != nil {
		t.Fatalf("SaveModelOverrides: %v", err)
	}

	// Reload from a fresh store (simulates process restart).
	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded := s2.LoadModelOverrides()
	if loaded.Len() != 1 {
		t.Fatalf("reloaded registry Len = %d, want 1", loaded.Len())
	}
	o := loaded.Get("tokenrouter", "deepseek-v4-flash")
	if o == nil {
		t.Fatal("override missing after reload")
	}
	if o.Vision == nil || *o.Vision != false {
		t.Error("vision not persisted")
	}
	if o.Context == nil || *o.Context != 1000000 {
		t.Error("context not persisted")
	}
	if o.Source != "review-agent" {
		t.Errorf("source = %q, want review-agent", o.Source)
	}
}

func TestModelOverridesAdapterRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &ModelOverrides{S: s}

	if adapter.Load().Len() != 0 {
		t.Fatal("initial load not empty")
	}

	r := domain.NewModelOverrideRegistry()
	_ = r.Set(&domain.ModelOverride{Provider: "p", Model: "m", Reasoning: boolPtrT(true)})
	if err := adapter.Save(r); err != nil {
		t.Fatalf("adapter Save: %v", err)
	}

	loaded := adapter.Load()
	if loaded.Len() != 1 {
		t.Fatalf("adapter Load Len = %d, want 1", loaded.Len())
	}
	o := loaded.Get("p", "m")
	if o == nil || o.Reasoning == nil || !*o.Reasoning {
		t.Error("reasoning override lost in round trip")
	}
}

func TestModelOverridesConcurrentSave(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &ModelOverrides{S: s}

	done := make(chan struct{}, 10)
	for i := 0; i < 10; i++ {
		go func(n int) {
			r := adapter.Load()
			_ = r.Set(&domain.ModelOverride{Provider: "p", Model: "m", Vision: boolPtrT(true)})
			_ = adapter.Save(r)
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	_ = adapter.Load()
}
