package modeloverrides

import (
	"testing"

	"nusashell/domain"
)

type fakeStore struct {
	registry *domain.ModelOverrideRegistry
	saves    int
}

func (f *fakeStore) Load() *domain.ModelOverrideRegistry {
	if f.registry == nil {
		return domain.NewModelOverrideRegistry()
	}
	return f.registry
}

func (f *fakeStore) Save(r *domain.ModelOverrideRegistry) error {
	f.saves++
	f.registry = r
	return nil
}

func boolP(b bool) *bool { return &b }
func intP(i int) *int    { return &i }

func TestCacheSetGetRemove(t *testing.T) {
	store := &fakeStore{}
	cache := New(store)

	if err := cache.Set(&domain.ModelOverride{
		Provider: "tokenrouter", Model: "deepseek-v4-flash",
		Vision: boolP(false), Context: intP(1000000),
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	o := cache.Get("tokenrouter", "deepseek-v4-flash")
	if o == nil {
		t.Fatal("Get returned nil after Set")
	}
	if o.Vision == nil || *o.Vision != false {
		t.Error("vision not stored")
	}
	if store.saves != 1 {
		t.Errorf("saves = %d, want 1", store.saves)
	}

	if !cache.Remove("tokenrouter", "deepseek-v4-flash") {
		t.Error("Remove should return true")
	}
	if cache.Get("tokenrouter", "deepseek-v4-flash") != nil {
		t.Error("entry should be gone after Remove")
	}
	if store.saves != 2 {
		t.Errorf("saves = %d, want 2", store.saves)
	}
}

func TestCacheSetRejectsInvalid(t *testing.T) {
	cache := New(&fakeStore{})
	if err := cache.Set(&domain.ModelOverride{Provider: "p", Model: "m"}); err == nil {
		t.Error("Set with no fields must be rejected")
	}
	if err := cache.Set(&domain.ModelOverride{Provider: "p", Model: "m", Context: intP(0)}); err == nil {
		t.Error("Set with zero context must be rejected")
	}
}

func TestCacheNilSafe(t *testing.T) {
	var cache *Cache
	if cache.Apply(&domain.Model{}, "p", "m") {
		t.Error("nil cache Apply must return false")
	}
	if cache.Get("p", "m") != nil {
		t.Error("nil cache Get must return nil")
	}
	if cache.List() != nil {
		t.Error("nil cache List must return nil")
	}
	if err := cache.Set(&domain.ModelOverride{Provider: "p", Model: "m", Vision: boolP(true)}); err != nil {
		t.Errorf("nil cache Set must be a no-op, got %v", err)
	}
	if cache.Remove("p", "m") {
		t.Error("nil cache Remove must return false")
	}
}
