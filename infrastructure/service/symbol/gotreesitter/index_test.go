//go:build repomap

package gotreesitter_test

import (
	"testing"

	svc "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	symgts "github.com/jahrulnr/nusashell/infrastructure/service/symbol/gotreesitter"
	"github.com/jahrulnr/nusashell/infrastructure/service/symbol/symboltest"
)

func TestContract(t *testing.T) {
	symboltest.Run(t, func() svc.SymbolIndex { return symgts.New(symgts.Options{}) })
}
