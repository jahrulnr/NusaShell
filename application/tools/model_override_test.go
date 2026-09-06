package tools

import (
	"strings"
	"testing"

	"nusashell/application/service/modeloverrides"
	"nusashell/domain"
)

type fakeModelOverrideStore struct {
	registry *domain.ModelOverrideRegistry
	saves    int
}

func (f *fakeModelOverrideStore) Load() *domain.ModelOverrideRegistry {
	if f.registry == nil {
		return domain.NewModelOverrideRegistry()
	}
	return f.registry
}

func (f *fakeModelOverrideStore) Save(r *domain.ModelOverrideRegistry) error {
	f.saves++
	f.registry = r
	return nil
}

func newCacheForOverrideTest(t *testing.T) *modeloverrides.Cache {
	t.Helper()
	return modeloverrides.New(&fakeModelOverrideStore{})
}

func TestExecuteModelOverrideSet(t *testing.T) {
	cache := newCacheForOverrideTest(t)
	out, snippet, err := ExecuteModelOverride(cache, []byte(
		`{"op":"set","provider":"tokenrouter","model":"deepseek/deepseek-v4-flash","vision":false,"context":1000000,"reason":"catalog wrong"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "saved override") {
		t.Errorf("output = %q, want saved confirmation", out)
	}
	if snippet == "" {
		t.Error("set must produce a mutation snippet")
	}
	o := cache.Get("tokenrouter", "deepseek/deepseek-v4-flash")
	if o == nil {
		t.Fatal("override not stored")
	}
	if o.Vision == nil || *o.Vision != false {
		t.Error("vision not stored")
	}
	if o.Context == nil || *o.Context != 1000000 {
		t.Error("context not stored")
	}
	if o.Source != "review-agent" {
		t.Errorf("source = %q, want review-agent", o.Source)
	}
}

func TestExecuteModelOverrideSetRejectsNoFields(t *testing.T) {
	cache := newCacheForOverrideTest(t)
	out, snippet, err := ExecuteModelOverride(cache, []byte(
		`{"op":"set","provider":"p","model":"m"}`))
	if err == nil {
		t.Error("set with no fields must fail")
	}
	if snippet != "" {
		t.Error("rejected set must not produce a snippet")
	}
	if !strings.Contains(out, "error") {
		t.Errorf("output = %q, want error message", out)
	}
}

func TestExecuteModelOverrideRemove(t *testing.T) {
	cache := newCacheForOverrideTest(t)
	_, _, _ = ExecuteModelOverride(cache, []byte(
		`{"op":"set","provider":"p","model":"m","vision":true}`))

	out, snippet, err := ExecuteModelOverride(cache, []byte(`{"op":"remove","provider":"p","model":"m"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "removed") {
		t.Errorf("output = %q, want removed confirmation", out)
	}
	if snippet == "" {
		t.Error("remove must produce a mutation snippet")
	}
	if cache.Get("p", "m") != nil {
		t.Error("override should be gone")
	}

	out, snippet, _ = ExecuteModelOverride(cache, []byte(`{"op":"remove","provider":"p","model":"m"}`))
	if snippet != "" {
		t.Error("no-op remove must not produce a snippet")
	}
	if !strings.Contains(out, "nothing removed") {
		t.Errorf("output = %q, want nothing-removed note", out)
	}
}

func TestExecuteModelOverrideList(t *testing.T) {
	cache := newCacheForOverrideTest(t)
	out, snippet, err := ExecuteModelOverride(cache, []byte(`{"op":"list"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snippet != "" {
		t.Error("list is read-only, must not produce a snippet")
	}
	if !strings.Contains(out, "no model overrides") {
		t.Errorf("empty list output = %q", out)
	}

	_, _, _ = ExecuteModelOverride(cache, []byte(`{"op":"set","provider":"p","model":"m","vision":false}`))
	out, _, _ = ExecuteModelOverride(cache, []byte(`{"op":"list"}`))
	if !strings.Contains(out, "p/m") || !strings.Contains(out, "vision=false") {
		t.Errorf("list output = %q, want p/m vision=false", out)
	}
}

func TestExecuteModelOverrideUnknownOp(t *testing.T) {
	cache := newCacheForOverrideTest(t)
	out, _, err := ExecuteModelOverride(cache, []byte(`{"op":"destroy"}`))
	if err == nil {
		t.Error("unknown op must fail")
	}
	if !strings.Contains(out, "unknown op") {
		t.Errorf("output = %q, want unknown-op error", out)
	}
}

func TestExecuteModelOverrideNilCache(t *testing.T) {
	out, _, err := ExecuteModelOverride(nil, []byte(`{"op":"list"}`))
	if err == nil {
		t.Error("nil cache must error")
	}
	if !strings.Contains(out, "not configured") {
		t.Errorf("output = %q, want not-configured error", out)
	}
}

func TestDescribeOverride(t *testing.T) {
	v := false
	c := 1000000
	o := &domain.ModelOverride{Vision: &v, Context: &c}
	got := DescribeOverride(o)
	if !strings.Contains(got, "vision=false") || !strings.Contains(got, "context=1000000") {
		t.Errorf("DescribeOverride = %q", got)
	}
	if DescribeOverride(nil) != "(none)" {
		t.Error("nil override must describe as (none)")
	}
}
