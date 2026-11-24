package wsconversation

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
)

// A tab that reattaches during a turn gets the live document instead of the
// journal it missed — and after a retry that document must agree with
// history: the attempt the turn replaced is gone, only the retried attempt's
// output is there. The client applies the same rule to its own view on
// retry_wait, so the server-side document has to as well.
func TestRetryWaitTruncatesLiveSnapshot(t *testing.T) {
	emitted := make(chan struct{})
	release := make(chan struct{})
	h := newHub(t, stubSender{fn: func(_ context.Context, convID, _, _, _ string, onEvent event.TurnEventHandler) (appconversation.SendResult, error) {
		// The failed attempt streamed more frames than one watch answer may
		// replay, so the reattach below is answered with the snapshot.
		for i := 0; i <= replayBurst; i++ {
			onEvent(event.NewDeltaEvent("withdrawn "))
		}
		onEvent(event.NewStatusEvent(enum.TurnStatusRetryWait, time.Millisecond))
		onEvent(event.NewDeltaEvent("the answer"))
		close(emitted)
		<-release
		return appconversation.SendResult{ConversationID: convID, UserMessagePersisted: true}, nil
	}})
	owner := dial(t, h)
	sendCommand(t, owner, `{"type":"turn.send","requestId":"s1","payload":{"content":"x"}}`)
	opened := readFrame(t, owner)
	if opened.Type != "turn.opened" {
		t.Fatalf("first frame = %+v, want turn.opened", opened)
	}
	<-emitted

	watcher := dial(t, h)
	sendCommand(t, watcher, fmt.Sprintf(
		`{"type":"turn.watch","requestId":"w1","conversationId":%q,"turnId":%q,"afterSeq":0}`,
		opened.ConversationID, opened.TurnID))
	f := readFrame(t, watcher)
	if f.Type != "turn.resync_required" {
		t.Fatalf("watch frame = %+v, want resync_required", f)
	}
	var snapshot resyncPayload
	if err := json.Unmarshal(f.Payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Turn != "active" {
		t.Fatalf("snapshot turn = %q, want active", snapshot.Turn)
	}
	if len(snapshot.Parts) != 1 || snapshot.Parts[0].Kind != "text" || snapshot.Parts[0].Text != "the answer" {
		t.Fatalf("snapshot parts = %+v, want only the retried attempt", snapshot.Parts)
	}
	if snapshot.Dropped != 0 {
		t.Fatalf("snapshot dropped = %d, want 0 — the replaced attempt is not partial recovery", snapshot.Dropped)
	}

	// The snapshot path still attaches: the tab sees the terminal.
	close(release)
	for {
		f := readFrame(t, watcher)
		if f.Type == "turn.completed" {
			return
		}
		if f.Type == "turn.failed" || f.Type == "error" {
			t.Fatalf("terminal frame = %+v", f)
		}
	}
}
