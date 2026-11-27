package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The settable leaves come from the marshaled defaults (application/setting
// schemaLeaf), so a fresh config must carry the default on the wire.
func TestSkillsImprovementDefaultsOffAndIsSettable(t *testing.T) {
	raw, err := json.Marshal(NewConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"skills":{"improvement":"off","catalog_max_bytes":32768}`) {
		t.Fatalf("defaults carry no settable skills leaves: %s", raw)
	}
}

// The catalog budget is the owner's. An omitted value takes the design
// default. A zero is unset, the same way every other integer leaf is, so it
// takes that default too. A negative is a value, and it is refused.
func TestSkillsCatalogBudgetIsPositiveAndSettable(t *testing.T) {
	cfg, err := Validate([]byte(`{"skills":{"improvement":"off"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Skills.CatalogMaxBytes != 32768 {
		t.Fatalf("omitted budget = %d, want 32768", cfg.Skills.CatalogMaxBytes)
	}
	cfg, err = Validate([]byte(`{"skills":{"catalog_max_bytes":0}}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Skills.CatalogMaxBytes != 32768 {
		t.Fatalf("zero budget = %d, want the default: zero is unset", cfg.Skills.CatalogMaxBytes)
	}
	cfg, err = Validate([]byte(`{"skills":{"catalog_max_bytes":4096}}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Skills.CatalogMaxBytes != 4096 {
		t.Fatalf("budget = %d, want 4096", cfg.Skills.CatalogMaxBytes)
	}
	if _, err := Validate([]byte(`{"skills":{"catalog_max_bytes":-1}}`)); err == nil {
		t.Fatal("a negative catalog budget was accepted")
	}
}

// off, observe, and suggest are the modes this build can honor. Anything
// else is refused, so a typo cannot select a mode that does not exist.
func TestSkillsImprovementAcceptsTheThreeModes(t *testing.T) {
	for _, tc := range []struct {
		mode string
		ok   bool
	}{{"off", true}, {"observe", true}, {"suggest", true}, {"on", false}} {
		cfg, err := Validate([]byte(fmt.Sprintf(`{"skills":{"improvement":%q}}`, tc.mode)))
		if got := err == nil; got != tc.ok {
			t.Fatalf("improvement %q accepted = %v, want %v (err %v)", tc.mode, got, tc.ok, err)
		}
		if !tc.ok {
			continue
		}
		raw, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), fmt.Sprintf(`"improvement":%q`, tc.mode)) {
			t.Fatalf("the written config lost improvement %q: %s", tc.mode, raw)
		}
	}
}
