package entity

import (
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// The length method must agree with the string it replaces — the estimate
// bills whatever adapters would replay, so a drift here silently
// miscounts context.
func TestOutputWithUntrustedTagLen(t *testing.T) {
	for _, c := range []struct {
		name    string
		output  string
		latency string
	}{
		{"empty", "", ""},
		{"small", "result ok", ""},
		{"multiline", "first line\nsecond line\n", "12ms"},
		{"unicode", "héllo — naïve — ", "1s"},
		{"json", `{"error":{"message":"bad key"}}`, "300ms"},
		{"near delimiter text", "uses <untrusted-tool> wording", ""},
	} {
		tc := mustCall(t, "i", "n", "{}", enum.TooltypeOrigin)
		tc.SetStoredOutput(c.output, c.latency)
		if got, want := tc.OutputWithUntrustedTagLen(), len(tc.OutputWithUntrustedTag()); got != want {
			t.Errorf("%s: OutputWithUntrustedTagLen = %d, want %d", c.name, got, want)
		}
	}
}

// Over-budget output exercises the mirrored window math — head slice,
// omitted-count marker digits, tail slice — without building the string.
func TestOutputWithUntrustedTagLenTruncated(t *testing.T) {
	tc := mustCall(t, "i", "n", "{}", enum.TooltypeOrigin)
	tc.SetOutputBudget(64)
	tc.SetStoredOutput(strings.Repeat("x", 200), "9ms")
	if got, want := tc.OutputWithUntrustedTagLen(), len(tc.OutputWithUntrustedTag()); got != want {
		t.Fatalf("truncated: OutputWithUntrustedTagLen = %d, want %d", got, want)
	}
	if tc.OutputWithUntrustedTagLen() >= 64+untrustedTagOverhead+len("9ms")+8 {
		t.Fatalf("len looks unbounded: %d", tc.OutputWithUntrustedTagLen())
	}
}

// A cut can land mid-rune; the head/tail boundary trim must be counted
// exactly, so unicode output right at the budget edge stays in lockstep.
func TestOutputWithUntrustedTagLenTruncatedUnicodeBoundary(t *testing.T) {
	tc := mustCall(t, "i", "n", "{}", enum.TooltypeOrigin)
	tc.SetOutputBudget(100)
	tc.SetStoredOutput(strings.Repeat("é", 60)+strings.Repeat("x", 200), "")
	if got, want := tc.OutputWithUntrustedTagLen(), len(tc.OutputWithUntrustedTag()); got != want {
		t.Fatalf("unicode cut: OutputWithUntrustedTagLen = %d, want %d", got, want)
	}
}

// A budget smaller than the marker itself takes the room<=0 branch — the
// window degenerates to a bare head slice and the length must track it.
func TestOutputWithUntrustedTagLenTinyBudget(t *testing.T) {
	tc := mustCall(t, "i", "n", "{}", enum.TooltypeOrigin)
	tc.SetOutputBudget(10)
	tc.SetStoredOutput(strings.Repeat("y", 200), "")
	if got, want := tc.OutputWithUntrustedTagLen(), len(tc.OutputWithUntrustedTag()); got != want {
		t.Fatalf("tiny budget: OutputWithUntrustedTagLen = %d, want %d", got, want)
	}
}

// A pending call has no envelope — OrInterrupted is what replay uses
// instead, but the raw tagged length stays zero either way.
func TestOutputWithUntrustedTagLenPending(t *testing.T) {
	tc := mustCall(t, "i", "n", "{}", enum.TooltypeOrigin)
	if tc.OutputWithUntrustedTagLen() != 0 || tc.OutputWithUntrustedTag() != "" {
		t.Fatalf("pending call measured %d", tc.OutputWithUntrustedTagLen())
	}
}
