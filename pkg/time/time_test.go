package time

import (
	"testing"
	stdtime "time"
)

func TestNewTimeUsesMachineLocalLocation(t *testing.T) {
	got := NewTime().Time()
	want := stdtime.Now().In(stdtime.Local)

	if got.Location() != stdtime.Local {
		t.Fatalf("location = %v, want machine location %v", got.Location(), stdtime.Local)
	}
	if got.Sub(want) > stdtime.Second || want.Sub(got) > stdtime.Second {
		t.Fatalf("time = %v, want close to machine time %v", got, want)
	}
}

func TestNewTimeProvidesCommonRepresentations(t *testing.T) {
	input := stdtime.Date(2026, stdtime.September, 1, 14, 5, 6, 789000000, stdtime.UTC)
	got := NewTime(input)

	if got.Time().Location() != stdtime.Local {
		t.Fatalf("location = %v, want machine location %v", got.Time().Location(), stdtime.Local)
	}
	if got.Epoch() != input.Unix() {
		t.Fatalf("epoch = %d, want %d", got.Epoch(), input.Unix())
	}
	if got.EpochMilli() != input.UnixMilli() {
		t.Fatalf("epoch milli = %d, want %d", got.EpochMilli(), input.UnixMilli())
	}
	if got.EpochMicro() != input.UnixMicro() {
		t.Fatalf("epoch micro = %d, want %d", got.EpochMicro(), input.UnixMicro())
	}
	if got.EpochNano() != input.UnixNano() {
		t.Fatalf("epoch nano = %d, want %d", got.EpochNano(), input.UnixNano())
	}
	if got.DMY() != got.Format("02/01/2006") {
		t.Fatalf("dmy = %q, want format equivalent %q", got.DMY(), got.Format("02/01/2006"))
	}
	if got.RFC3339() != got.Format(stdtime.RFC3339) {
		t.Fatalf("rfc3339 = %q, want format equivalent %q", got.RFC3339(), got.Format(stdtime.RFC3339))
	}
	if got.RFC3339Nano() != got.Format(stdtime.RFC3339Nano) {
		t.Fatalf("rfc3339 nano = %q, want format equivalent %q", got.RFC3339Nano(), got.Format(stdtime.RFC3339Nano))
	}
}

func TestTimeProvidesElapsedDurations(t *testing.T) {
	current := stdtime.Date(2026, stdtime.September, 1, 14, 5, 6, 0, stdtime.UTC)
	if got := NewTime(current).Since(current.Add(-2 * stdtime.Second)); got != 2*stdtime.Second {
		t.Fatalf("since = %s, want 2s", got)
	}
	if got := NewTime(current).Until(current.Add(3 * stdtime.Second)); got != 3*stdtime.Second {
		t.Fatalf("until = %s, want 3s", got)
	}
}
