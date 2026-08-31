package yamlmd

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	clock "nusashell/pkg/time"
)

func TestYAMLV3PanicsOnNilTimePointerInMap(t *testing.T) {
	// yaml.v3 type-switches *time.Time before checking for a typed-nil
	// inside interface{}, then timev asserts the elem is time.Time.
	var wake *time.Time
	defer func() {
		if recover() == nil {
			t.Fatal("expected yaml.v3 to panic on nil *time.Time in map[string]any")
		}
	}()
	_, _ = yaml.Marshal(map[string]any{"wake_at": wake})
}

func TestBlockDoesNotPanicOnNilTimePointerInMap(t *testing.T) {
	var wake *time.Time
	got := Block(map[string]any{
		"run_id":  "run_1",
		"status":  "success",
		"wake_at": wake,
	})
	if strings.Contains(got, "marshal error") {
		t.Fatalf("Block must not surface yaml.v3 panic as marshal error:\n%s", got)
	}
	if !strings.Contains(got, "run_id: run_1") && !strings.Contains(got, "run_id: \"run_1\"") {
		t.Fatalf("expected run_id in output:\n%s", got)
	}
}

func TestBlockMarshalsTimePointerInMapAsRFC3339(t *testing.T) {
	wake := time.Date(2026, 8, 31, 2, 0, 0, 0, time.UTC)
	got := Block(map[string]any{"wake_at": &wake})
	if strings.Contains(got, "marshal error") {
		t.Fatalf("Block must not panic on non-nil *time.Time in map:\n%s", got)
	}
	if !strings.Contains(got, clock.NewTime(wake).RFC3339()) {
		t.Fatalf("expected RFC3339 wake_at, got:\n%s", got)
	}
}

func TestBlockMarshalsNestedNilTimePointer(t *testing.T) {
	got := Block(map[string]any{
		"nested": map[string]any{"wake_at": (*time.Time)(nil)},
		"items":  []any{(*time.Time)(nil), "ok"},
	})
	if strings.Contains(got, "marshal error") {
		t.Fatalf("Block must walk nested maps/slices:\n%s", got)
	}
}
