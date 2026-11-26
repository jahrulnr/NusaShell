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
	if !strings.Contains(string(raw), `"skills":{"improvement":"off"}`) {
		t.Fatalf("defaults carry no settable skills.improvement: %s", raw)
	}
}

// Only off and observe exist in this build: suggest is refused until the
// proposal flow gives it behavior, so no mode is accepted without one.
func TestSkillsImprovementAcceptsOffAndObserveOnly(t *testing.T) {
	for _, tc := range []struct {
		mode string
		ok   bool
	}{{"off", true}, {"observe", true}, {"suggest", false}, {"on", false}} {
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
