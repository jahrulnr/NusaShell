package domain

import "testing"

func TestDefaultMaxParallelTools(t *testing.T) {
	if DefaultMaxParallelTools != 6 {
		t.Fatalf("DefaultMaxParallelTools = %d, want 6", DefaultMaxParallelTools)
	}
	if DefaultSettings().MaxParallelTools != DefaultMaxParallelTools {
		t.Fatalf("DefaultSettings().MaxParallelTools = %d, want %d", DefaultSettings().MaxParallelTools, DefaultMaxParallelTools)
	}
}
