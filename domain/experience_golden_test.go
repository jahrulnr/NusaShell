package domain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestExperienceGoldenRoundTrip(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "testdata", "golden", "experience.json")
	b, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	var exp Experience
	if err := json.Unmarshal(b, &exp); err != nil {
		t.Fatal(err)
	}
	if exp.Goal != "debug nginx upload" {
		t.Fatalf("goal=%q", exp.Goal)
	}
	if len(exp.Corrections) != 1 {
		t.Fatalf("corrections=%d", len(exp.Corrections))
	}
	if _, err := json.Marshal(exp); err != nil {
		t.Fatal(err)
	}
	if exp.Timestamp.IsZero() {
		t.Fatal("timestamp")
	}
	_ = time.RFC3339
}
