package domain

import "testing"

func TestTrustLevelToRiskTierCap(t *testing.T) {
	cases := []struct {
		trust TrustLevel
		want  RiskTier
	}{
		{TrustSafe, RiskReadOnly},
		{TrustTrusted, RiskEditConfirmed},
		{TrustPrivileged, RiskBypass},
		{"", RiskReadOnly},      // default safe
		{"unknown", RiskReadOnly}, // unknown falls to safe
	}
	for _, c := range cases {
		got := TrustLevelToRiskTierCap(c.trust)
		if got != c.want {
			t.Errorf("TrustLevelToRiskTierCap(%q) = %q, want %q", c.trust, got, c.want)
		}
	}
}
