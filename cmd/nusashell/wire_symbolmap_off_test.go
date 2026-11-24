//go:build !repomap

package main

import (
	"testing"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
)

// Without the repomap tag the wire registers nothing — no tool, no
// record, no "unavailable" noise.
func TestWireSymbolMapLeanRegistersNothing(t *testing.T) {
	tools := map[string]dservice.Tool{}
	if err := wireSymbolMap(tools, "", t.TempDir(), repoworkspace.NewLocal(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if len(tools) != 0 {
		t.Fatalf("lean build must register no tools, got %d", len(tools))
	}
	if _, ok := tools["symbol_map"]; ok {
		t.Fatal("symbol_map must be absent without the repomap tag")
	}
}
