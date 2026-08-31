package domain

import "testing"

func TestIsDelegateResultCallID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"delegate-result-run_abc", true},
		{"delegate-result-", true},
		{"delegate-result", false},
		{"subagent-result-acprun_abc", false},
		{"call_abc", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsDelegateResultCallID(c.id); got != c.want {
			t.Errorf("IsDelegateResultCallID(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}

func TestDelegateResultArgsCarriesIDs(t *testing.T) {
	args := DelegateResultArgs("run_del", "conv_del")
	if args == "" {
		t.Fatal("args must not be empty")
	}
	if len(DelegateBriefResult("run_del", true)) == 0 || len(DelegateBriefResult("run_del", false)) == 0 {
		t.Fatal("brief results must not be empty")
	}
}
