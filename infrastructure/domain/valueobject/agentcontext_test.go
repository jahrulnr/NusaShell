package valueobject

import (
	"math"
	"testing"
)

func TestNewAgentContext(t *testing.T) {
	a, err := NewAgentContext(1280, 200000, 0.0123, " usd ")
	if err != nil {
		t.Fatal(err)
	}
	if a.Used() != 1280 || a.Size() != 200000 || a.IsZero() {
		t.Fatalf("got used %d size %d zero %v", a.Used(), a.Size(), a.IsZero())
	}
	if amount, cur, ok := a.Cost(); !ok || amount != 0.0123 || cur != "USD" {
		t.Fatalf("cost = %v %q %v", amount, cur, ok)
	}
	// An over-full context is a report, not an error.
	if _, err := NewAgentContext(210000, 200000, 0, ""); err != nil {
		t.Fatalf("used above size refused: %v", err)
	}
}

func TestNewAgentContextWithoutCost(t *testing.T) {
	a, err := NewAgentContext(10, 100, 5, "")
	if err != nil {
		t.Fatal(err)
	}
	// An amount with no currency is no cost at all.
	if _, _, ok := a.Cost(); ok {
		t.Fatal("a cost without a currency was kept")
	}
}

func TestNewAgentContextRefusesUnusableReports(t *testing.T) {
	for name, c := range map[string]struct {
		used, size int
		amount     float64
		currency   string
	}{
		"no window":         {used: 5, size: 0},
		"negative window":   {used: 5, size: -1},
		"negative used":     {used: -1, size: 100},
		"negative cost":     {used: 1, size: 100, amount: -1, currency: "USD"},
		"NaN cost":          {used: 1, size: 100, amount: math.NaN(), currency: "USD"},
		"infinite cost":     {used: 1, size: 100, amount: math.Inf(1), currency: "USD"},
		"overlong currency": {used: 1, size: 100, amount: 1, currency: "TOOLONGCUR"},
	} {
		if _, err := NewAgentContext(c.used, c.size, c.amount, c.currency); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestZeroAgentContextIsEmpty(t *testing.T) {
	if !(AgentContext{}).IsZero() {
		t.Fatal("the zero value must read as nothing reported")
	}
}
