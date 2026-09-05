package application

import (
	"strings"
	"testing"

	"nusashell/application/service/modeloverrides"
	"nusashell/domain"
)

func newAppForOverrideTest(t *testing.T) *App {
	t.Helper()
	return &App{modelOverrides: modeloverrides.New(&fakeModelOverrideStore{})}
}

func TestExecuteModelOverrideSet(t *testing.T) {
	r := newAppForOverrideTest(t)
	out, snippet, err := r.executeModelOverride([]byte(
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
	o := r.modelOverrides.Get("tokenrouter", "deepseek/deepseek-v4-flash")
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
	r := newAppForOverrideTest(t)
	out, snippet, err := r.executeModelOverride([]byte(
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
	r := newAppForOverrideTest(t)
	_, _, _ = r.executeModelOverride([]byte(
		`{"op":"set","provider":"p","model":"m","vision":true}`))

	out, snippet, err := r.executeModelOverride([]byte(`{"op":"remove","provider":"p","model":"m"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "removed") {
		t.Errorf("output = %q, want removed confirmation", out)
	}
	if snippet == "" {
		t.Error("remove must produce a mutation snippet")
	}
	if r.modelOverrides.Get("p", "m") != nil {
		t.Error("override should be gone")
	}

	// Removing again is a no-op with no snippet.
	out, snippet, _ = r.executeModelOverride([]byte(`{"op":"remove","provider":"p","model":"m"}`))
	if snippet != "" {
		t.Error("no-op remove must not produce a snippet")
	}
	if !strings.Contains(out, "nothing removed") {
		t.Errorf("output = %q, want nothing-removed note", out)
	}
}

func TestExecuteModelOverrideList(t *testing.T) {
	r := newAppForOverrideTest(t)
	out, snippet, err := r.executeModelOverride([]byte(`{"op":"list"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snippet != "" {
		t.Error("list is read-only, must not produce a snippet")
	}
	if !strings.Contains(out, "no model overrides") {
		t.Errorf("empty list output = %q", out)
	}

	_, _, _ = r.executeModelOverride([]byte(`{"op":"set","provider":"p","model":"m","vision":false}`))
	out, _, _ = r.executeModelOverride([]byte(`{"op":"list"}`))
	if !strings.Contains(out, "p/m") || !strings.Contains(out, "vision=false") {
		t.Errorf("list output = %q, want p/m vision=false", out)
	}
}

func TestExecuteModelOverrideUnknownOp(t *testing.T) {
	r := newAppForOverrideTest(t)
	out, _, err := r.executeModelOverride([]byte(`{"op":"destroy"}`))
	if err == nil {
		t.Error("unknown op must fail")
	}
	if !strings.Contains(out, "unknown op") {
		t.Errorf("output = %q, want unknown-op error", out)
	}
}

func TestExecuteModelOverrideNilCache(t *testing.T) {
	r := &App{} // modelOverrides is nil
	out, _, err := r.executeModelOverride([]byte(`{"op":"list"}`))
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
	got := describeOverride(o)
	if !strings.Contains(got, "vision=false") || !strings.Contains(got, "context=1000000") {
		t.Errorf("describeOverride = %q", got)
	}
	if describeOverride(nil) != "(none)" {
		t.Error("nil override must describe as (none)")
	}
}
