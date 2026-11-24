package regex_test

import (
	"testing"

	svc "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/service/symbol/regex"
	"github.com/jahrulnr/nusashell/infrastructure/service/symbol/symboltest"
)

func TestContract(t *testing.T) {
	symboltest.Run(t, func() svc.SymbolIndex { return regex.New(0) })
}
