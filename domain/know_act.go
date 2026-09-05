package domain

import "strings"

// KnowActScore separates retrieval (Know) from utilization (Act).
type KnowActScore struct {
	Retrieved bool
	Applied   bool
}

func (k KnowActScore) Label() string {
	switch {
	case k.Retrieved && k.Applied:
		return "know_and_act"
	case k.Retrieved && !k.Applied:
		return "know_not_act"
	case !k.Retrieved && k.Applied:
		return "act_without_know"
	default:
		return "miss"
	}
}

// ScoreKnowAct reports whether expected knowledge was retrieved and whether
// the APPLY block actually contains the utilization needle.
func ScoreKnowAct(retrievedIDs []string, expectedID, applyBlock, applyNeedle string) KnowActScore {
	got := false
	for _, id := range retrievedIDs {
		if id == expectedID {
			got = true
			break
		}
	}
	applied := applyNeedle != "" && strings.Contains(applyBlock, applyNeedle)
	return KnowActScore{Retrieved: got, Applied: applied}
}
