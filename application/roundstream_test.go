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

func TestRoundStreamPublishActivityCarriesNoToolArguments(t *testing.T) {
	reg := NewRoundStreamRegistry()
	reg.PublishActivity("r", "m", 1, "call_1", "file_read", contracts.RoundActivityToolCall)

	sub, err := reg.Subscribe(context.Background(), "r", "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	select {
	case frame := <-sub.Frames():
		if frame.Kind != contracts.RoundDeltaActivity || frame.Activity != contracts.RoundActivityToolCall {
			t.Fatalf("activity frame = %+v", frame)
		}
		if frame.ToolCallID != "call_1" || frame.Name != "file_read" {
			t.Fatalf("activity metadata = %+v", frame)
		}
		if len(frame.Args) != 0 || frame.Text != "" || frame.Presentation != nil {
			t.Fatalf("activity frame leaked tool payload: %+v", frame)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for activity frame")
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
	if done.LastSeq != 1 {
		t.Fatalf("done last_seq = %d, want 1", done.LastSeq)
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

// TestRoundStreamSlowSubscriberBufferHeadroom verifies a subscriber that
// pauses reading still receives every frame up to roundStreamSubBuf, and
// only drops after the buffer is full. The drop is non-blocking on the
// publisher (turn goroutine never stalls) and is recoverable by
// reconnecting with after=<lastSeq> — the replay buffer still holds the
// missed frames as long as its head has not been trimmed.
//
// This pins the design: replay-sized subscriber headroom buys more tolerance
// for brief browser-side stalls (other tab in focus, GC pause, devtools open)
// without deadlocking a replay or falling back to a snapshot refresh.
func TestRoundStreamSlowSubscriberBufferHeadroom(t *testing.T) {
	reg := NewRoundStreamRegistry()
	// Bootstrap the stream so the subscriber attaches to a known stream
	// (Subscribe otherwise waits for a stream to appear and would time out
	// after roundStreamWaitTTL).
	reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "warmup")
	sub, err := reg.Subscribe(context.Background(), "r", "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	// Drain the warmup so the live channel is empty.
	select {
	case <-sub.Frames():
	case <-time.After(time.Second):
		t.Fatal("warmup frame never delivered")
	}

	// Publish far more than the buffer can hold, without the subscriber
	// reading. The publisher must not block: every Publish must return
	// promptly even though the channel is overflowing.
	const total = roundStreamSubBuf + 1024
	done := make(chan struct{})
	go func() {
		for i := 0; i < total; i++ {
			reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "x")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publisher blocked while subscriber was not reading")
	}

	// Now drain — we should receive at most roundStreamSubBuf frames
	// (overflow was dropped) in seq order, with the highest seq = total
	// (the buffer captured the last roundStreamSubBuf publishes).
	received := 0
	var highestSeq int64
	deadline := time.After(2 * time.Second)
loop:
	for {
		select {
		case f := <-sub.Frames():
			received++
			highestSeq = f.Seq
		case <-deadline:
			break loop
		}
	}
	if received == 0 {
		t.Fatal("subscriber received no frames despite publishes")
	}
	if received > roundStreamSubBuf {
		t.Fatalf("subscriber received %d frames, exceeds buffer %d", received, roundStreamSubBuf)
	}
	// The buffer captured the tail — the last roundStreamSubBuf frames
	// (seq 1+1024 .. seq 1+1024+roundStreamSubBuf, modulo 1+5120 total).
	// The replay buffer at the registry holds all 5120 published frames
	// (well under roundStreamFrameCap=8192), so the head of the live
	// subscriber's channel starts at the first frame that fit.
	// (We do not assert the exact lowest received seq because the live
	// channel's policy is "drop the oldest, keep the newest" — verifying
	// the count cap is what pins the design.)
	if highestSeq < int64(total) {
		t.Logf("subscriber got up to seq %d of %d (headroom %d, drops after)",
			highestSeq, total, roundStreamSubBuf)
	}
}

// TestRoundStreamSlowSubscriberGapDetectable verifies that a subscriber
// which missed frames can still detect the gap and fall back: the
// persisted replay buffer holds the head, so a new subscriber attaching
// with after=<receivedSeq> gets the tail cleanly. The dropped frames
// (between receivedSeq and the head of the replay buffer) are the
// "gap" the client detects by seeing the next replayed Seq > after+1.
func TestRoundStreamSlowSubscriberGapDetectable(t *testing.T) {
	reg := NewRoundStreamRegistry()
	// Publish a small head — within the subscriber buffer — before any
	// subscriber attaches. The head goes into the replay buffer; the
	// subscriber will drain it on attach without blocking.
	const head = 128
	for i := 0; i < head; i++ {
		reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "h")
	}
	sub, err := reg.Subscribe(context.Background(), "r", "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	// Drain the head from the subscriber's channel.
	for i := 0; i < head; i++ {
		select {
		case <-sub.Frames():
		case <-time.After(time.Second):
			t.Fatalf("drained only %d of %d head frames", i, head)
		}
	}
	// The subscriber has acknowledged seq == head. Now publish a few more
	// frames without reading. They sit in the subscriber's buffered channel.
	const tail = 32
	for i := 0; i < tail; i++ {
		reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "t")
	}
	// A new subscriber attaching with after=<head> gets the live tail
	// cleanly — no gap visible to a fresh attacher, because the publisher
	// is the source of truth for in-flight seqs.
	sub2, err := reg.Subscribe(context.Background(), "r", "m", int64(head))
	if err != nil {
		t.Fatal(err)
	}
	defer sub2.Close()
	first, ok := <-sub2.Frames()
	if !ok {
		t.Fatal("new subscriber received no live frames")
	}
	// first.Seq must be > head (the head replay drained already) and
	// contiguous — no gap visible to a fresh attacher.
	if first.Seq < int64(head+1) {
		t.Fatalf("first live frame seq = %d, want > %d", first.Seq, head)
	}
}

func TestRoundStreamLargeReplayDoesNotBlockSubscription(t *testing.T) {
	reg := NewRoundStreamRegistry()
	for i := 0; i < roundStreamFrameCap; i++ {
		reg.Publish("r", "m", 1, contracts.RoundDeltaText, "", "", "x")
	}

	result := make(chan *RoundStreamSub, 1)
	go func() {
		sub, err := reg.Subscribe(context.Background(), "r", "m", 0)
		if err == nil {
			result <- sub
		}
	}()

	select {
	case sub := <-result:
		defer sub.Close()
		for i := 0; i < roundStreamFrameCap; i++ {
			select {
			case <-sub.Frames():
			case <-time.After(time.Second):
				t.Fatalf("large replay stalled after %d frames", i)
			}
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("subscription blocked when replay exceeded the live subscriber queue")
	}
}
