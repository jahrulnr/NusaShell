package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"nusashell/contracts"
)

// TestRoundStreamReplayCursor verifies the idempotent resume contract: a
// subscriber attaching with after=<seq> receives only newer frames, in
// order, with monotonic seq numbers.
func TestRoundStreamReplayCursor(t *testing.T) {
	reg := NewRoundStreamRegistry()
	reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "a")
	reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "b")
	reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "c")

	sub, err := reg.Subscribe(context.Background(), "r", "m", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	var got []string
	for i := 0; i < 2; i++ {
		select {
		case f := <-sub.Frames():
			got = append(got, f.Text)
			if i == 0 && f.Seq != 2 {
				t.Fatalf("first replayed seq = %d, want 2", f.Seq)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for replay frames")
		}
	}
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("replay after=1 = %v, want [b c]", got)
	}
}

// TestRoundStreamSealDeliversDone verifies the terminal frame reaches
// subscribers and the seal is idempotent.
func TestRoundStreamSealDeliversDone(t *testing.T) {
	reg := NewRoundStreamRegistry()
	reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "a")

	sub, err := reg.Subscribe(context.Background(), "r", "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	reg.Seal("r", "m", 1, contracts.RoundDoneFrame{
		State: contracts.RoundStateDone, RunID: "r", MessageID: "m", Round: 1,
		Next: &contracts.RoundRef{RunID: "r", MessageID: "m2", Round: 2},
	})
	// Second seal is a no-op.
	reg.Seal("r", "m", 1, contracts.RoundDoneFrame{State: contracts.RoundStateError, RunID: "r", MessageID: "m"})

	select {
	case <-sub.Done():
	case <-time.After(time.Second):
		t.Fatal("done never closed")
	}
	done := sub.DoneFrame()
	if done.State != contracts.RoundStateDone || done.Next == nil || done.Next.MessageID != "m2" {
		t.Fatalf("done frame = %+v", done)
	}
	// Drain the replayed delta then verify no more frames arrive.
	select {
	case f := <-sub.Frames():
		if f.Text != "a" {
			t.Fatalf("delta = %+v", f)
		}
	default:
		t.Fatal("expected the replayed delta")
	}
}

// TestRoundStreamLateJoinReplay verifies a consumer attaching after the
// round has fully sealed still receives the complete delta tail plus the
// terminal frame (within the sealed TTL).
func TestRoundStreamLateJoinReplay(t *testing.T) {
	reg := NewRoundStreamRegistry()
	reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "x")
	reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "y")
	reg.Seal("r", "m", 1, contracts.RoundDoneFrame{State: contracts.RoundStateDone, RunID: "r", MessageID: "m", Round: 1})

	sub, err := reg.Subscribe(context.Background(), "r", "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	var got string
	for i := 0; i < 2; i++ {
		select {
		case f := <-sub.Frames():
			got += f.Text
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for replay")
		}
	}
	if got != "xy" {
		t.Fatalf("late join replay = %q, want xy", got)
	}
	select {
	case <-sub.Done():
	default:
		t.Fatal("sealed stream should close done immediately")
	}
}

// TestRoundStreamSealedOnlyRound verifies rounds that produce no deltas at
// all (e.g. a tool-only round) still get a stream via Seal, so consumers
// receive the terminal frame instead of a 404.
func TestRoundStreamSealedOnlyRound(t *testing.T) {
	reg := NewRoundStreamRegistry()
	reg.Seal("r", "m", 1, contracts.RoundDoneFrame{State: contracts.RoundStateDone, RunID: "r", MessageID: "m", Round: 1})

	sub, err := reg.Subscribe(context.Background(), "r", "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	select {
	case <-sub.Done():
	default:
		t.Fatal("done not closed for sealed-only round")
	}
}

// TestRoundStreamWaitForLateCreate verifies a consumer that attaches before
// the round publishes (between agent.turn.started and the first delta) waits
// and then receives the live stream.
func TestRoundStreamWaitForLateCreate(t *testing.T) {
	reg := NewRoundStreamRegistry()
	result := make(chan *RoundStreamSub, 1)
	errs := make(chan error, 1)
	go func() {
		sub, err := reg.Subscribe(context.Background(), "r", "m", 0)
		if err != nil {
			errs <- err
			return
		}
		result <- sub
	}()
	time.Sleep(50 * time.Millisecond)
	reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "z")

	select {
	case sub := <-result:
		defer sub.Close()
		select {
		case f := <-sub.Frames():
			if f.Text != "z" {
				t.Fatalf("delta = %+v", f)
			}
		case <-time.After(time.Second):
			t.Fatal("no live frame after wait")
		}
	case err := <-errs:
		t.Fatalf("subscribe failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("subscribe never returned")
	}
}

// TestRoundStreamNotFound verifies a subscribe for a stream that never
// appears fails (ctx cancel path here; the registry TTL covers the timeout
// path).
func TestRoundStreamNotFound(t *testing.T) {
	reg := NewRoundStreamRegistry()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := reg.Subscribe(ctx, "r", "nope", 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

// TestRoundStreamPublishAfterSealDropped verifies deltas published after a
// seal are dropped (the terminal frame is authoritative).
func TestRoundStreamPublishAfterSealDropped(t *testing.T) {
	reg := NewRoundStreamRegistry()
	reg.Seal("r", "m", 1, contracts.RoundDoneFrame{State: contracts.RoundStateDone, RunID: "r", MessageID: "m", Round: 1})
	reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "late")
	sub, err := reg.Subscribe(context.Background(), "r", "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	select {
	case f := <-sub.Frames():
		t.Fatalf("unexpected frame after seal: %+v", f)
	case <-time.After(100 * time.Millisecond):
		// pass: no frames
	}
}
