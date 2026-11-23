package acp

import (
	"reflect"
	"testing"
)

// A Windows kill has to name the tree, not one process: the agent is a CLI
// that spawns its own children, and killing only the direct child leaves
// them running after the agent is gone.
func TestTaskkillArgs(t *testing.T) {
	got := taskkillArgs(4242)
	want := []string{"/T", "/F", "/PID", "4242"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("taskkillArgs(4242) = %v, want %v", got, want)
	}
}
