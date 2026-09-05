package application

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestCloseWaitsForLearningGoroutines(t *testing.T) {
	app := &App{}
	started := make(chan struct{})
	release := make(chan struct{})
	var ran atomic.Bool
	app.goSafe("learning", func() {
		close(started)
		<-release
		ran.Store(true)
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("learning goroutine did not start")
	}

	closed := make(chan struct{})
	go func() {
		app.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned before the learning goroutine finished")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after the learning goroutine finished")
	}
	if !ran.Load() {
		t.Fatal("learning goroutine did not run to completion")
	}
}

func TestCloseDoesNotWaitForUntrackedGoSafe(t *testing.T) {
	app := &App{}
	release := make(chan struct{})
	app.goSafe("agent", func() { <-release })
	closed := make(chan struct{})
	go func() {
		app.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close blocked on a non-learning goroutine")
	}
	close(release)
}
