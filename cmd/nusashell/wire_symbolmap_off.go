//go:build !repomap

package main

import (
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// wireSymbolMap is the lean-build counterpart: no repomap tag means no
// parser in the binary, so it registers nothing — the tool, its record,
// and its cost all stay absent rather than surfacing an "unavailable"
// stub.
func wireSymbolMap(map[string]dservice.Tool, string, string, drepo.Workspace, string) error {
	return nil
}
