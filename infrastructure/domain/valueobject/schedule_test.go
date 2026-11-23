package valueobject

import "testing"

func TestCronScheduleRefusesBadInput(t *testing.T) {
	for _, c := range []struct{ expr, zone string }{
		{"0 9 * *", "UTC"},            // four fields
		{"0 9 * * * *", "UTC"},        // six fields
		{"61 9 * * *", "UTC"},         // minute out of range
		{"0 9 * * *", "Mars/Olympus"}, // unknown zone
		{"0 9 * * *", ""},
		{"0 0 30 2 *", "UTC"}, // Feb 30 never fires
	} {
		if _, err := NewCronSchedule(c.expr, c.zone); err == nil {
			t.Errorf("%q in %q must be refused", c.expr, c.zone)
		}
	}
	if _, err := NewCronSchedule("*/15  9-17 * * 1-5", "Asia/Jakarta"); err != nil {
		t.Fatal(err)
	}
}
