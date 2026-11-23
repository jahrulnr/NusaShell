package browser

import (
	"context"
	"log/slog"
	"testing"

	"github.com/chromedp/cdproto/network"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// A viewer that falls behind only needs the newest state: a full buffer
// drops its oldest frame, never the update itself — and with room left a
// state never displaces buffered frames.
func TestLatestStateReachesAFloodedViewer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Session{ctx: ctx, cancel: cancel, log: slog.Default(),
		device:   dservice.BrowserDevice{Preset: "responsive", Width: 1280, Height: 800, DPR: 1},
		watchers: map[chan dservice.BrowserFrame]struct{}{}, netPending: map[network.RequestID]*NetEntry{}}

	ch := s.Watch(ctx)
	if f := <-ch; f.State == nil {
		t.Fatal("a viewer's first frame is the current state")
	}

	// With room in the buffer a state goes behind the buffered frames, it
	// does not evict them.
	s.broadcast(dservice.BrowserFrame{JPEG: []byte{42}})
	s.SetAgentLock(true)
	if f := <-ch; len(f.JPEG) != 1 || f.JPEG[0] != 42 {
		t.Fatalf("buffered frame = %+v, want the JPEG", f)
	}
	if f := <-ch; f.State == nil || !f.State.Lease.AgentLock {
		t.Fatalf("next frame = %+v, want the lock state", f)
	}

	// Flooded, the buffer drops its oldest frame so the newest state lands.
	for i := range watcherBuffer * 2 {
		s.broadcast(dservice.BrowserFrame{JPEG: []byte{byte(i)}})
	}
	s.SetAgentLock(false)
	var last dservice.BrowserFrame
	for {
		select {
		case last = <-ch:
		default:
			if last.State == nil || last.State.Lease.AgentLock {
				t.Fatalf("last buffered frame = %+v, want the fresh unlocked state", last)
			}
			// A subscriber that joins now hears the latest device and lease.
			next := <-s.Watch(ctx)
			if next.State == nil || next.State.Device.Preset != "responsive" || next.State.Lease.AgentLock {
				t.Fatalf("a new watcher's first frame = %+v", next)
			}
			return
		}
	}
}
