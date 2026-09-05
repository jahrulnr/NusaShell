package domain

import "testing"

func TestScoreKnowActLabels(t *testing.T) {
	if got := ScoreKnowAct([]string{"mem_1"}, "mem_1", "APPLY\n- prefer Go", "prefer Go").Label(); got != "know_and_act" {
		t.Fatalf("label=%s", got)
	}
	if got := ScoreKnowAct([]string{"mem_1"}, "mem_1", "APPLY\n- other", "prefer Go").Label(); got != "know_not_act" {
		t.Fatalf("label=%s", got)
	}
	if got := ScoreKnowAct(nil, "mem_1", "", "prefer Go").Label(); got != "miss" {
		t.Fatalf("label=%s", got)
	}
}
