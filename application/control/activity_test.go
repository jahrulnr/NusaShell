package appcontrol

import (
	"context"
	"sync"
	"testing"
	"time"
)

// The ring records real calls newest-first — metadata only (caller,
// tool, target, outcome, ms); a failing call lands as outcome "error".
func TestActivityRing(t *testing.T) {
	app, _, _ := setup(t)
	ctx := context.Background()

	if _, err := app.List(ctx, "c_self", ListInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Read(ctx, "c_self", ReadInput{Thread: "c_other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Read(ctx, "c_self", ReadInput{Thread: "@nobody"}); err == nil {
		t.Fatal("read of a missing thread must fail")
	}
	entries := app.Activity(ActivityFilter{})
	if len(entries) != 3 {
		t.Fatalf("entries = %+v", entries)
	}
	// Newest first: the failed read leads; a target that never resolved
	// to a thread id is recorded empty, like a List call's.
	if entries[0].Tool != "read" || entries[0].Outcome != "error" || entries[0].Target != "" {
		t.Fatalf("newest = %+v", entries[0])
	}
	if entries[1].Tool != "read" || entries[1].Outcome != "ok" || entries[1].Target != "c_other" {
		t.Fatalf("middle = %+v", entries[1])
	}
	if entries[2].Tool != "list" || entries[2].Outcome != "ok" || entries[2].Caller != "c_self" {
		t.Fatalf("oldest = %+v", entries[2])
	}

	// Filters narrow without copying the ring.
	if got := app.Activity(ActivityFilter{Caller: "c_self", Limit: 1}); len(got) != 1 || got[0].Outcome != "error" {
		t.Fatalf("caller+limit = %+v", got)
	}
	if got := app.Activity(ActivityFilter{Target: "c_other"}); len(got) != 1 || got[0].Tool != "read" {
		t.Fatalf("target filter = %+v", got)
	}

	// The bound holds: writing past activityCap drops the oldest.
	app.Clear()
	for range activityCap + 50 {
		app.note("c_a", "wait", "c_b", time.Now(), nil)
	}
	if got := app.Activity(ActivityFilter{}); len(got) != activityCap {
		t.Fatalf("ring = %d, want %d", len(got), activityCap)
	}

	// Concurrent writers and readers stay safe and bounded (-race).
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				app.note("c_a", "send", "c_b", time.Now(), nil)
				app.Activity(ActivityFilter{Limit: 5})
			}
		})
	}
	wg.Wait()
	if got := app.Activity(ActivityFilter{}); len(got) != activityCap {
		t.Fatalf("ring after races = %d, want %d", len(got), activityCap)
	}
	app.Clear()
	if got := app.Activity(ActivityFilter{}); len(got) != 0 {
		t.Fatalf("cleared ring = %d", len(got))
	}
}
