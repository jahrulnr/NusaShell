package aggregate

import (
	"testing"
	"time"
)

func TestProjectHandle(t *testing.T) {
	p, err := NewProject("p_1", "  Release  Prep\tQ4 ", []string{"/srv/app"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Handle(); got != "release-prep-q4" {
		t.Fatalf("handle = %q", got)
	}
}
