package skill

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	"github.com/jahrulnr/nusashell/infrastructure/redact"
)

// loadOnly is the observer's whole view of the store. Every other repository
// method is nil, so a call to one of them panics and fails the test.
type loadOnly struct {
	drepo.Conversation
	conv *aggregate.Conversation
	err  error
	ctx  context.Context
}

func (f *loadOnly) Load(ctx context.Context, _ string) (*aggregate.Conversation, error) {
	f.ctx = ctx
	return f.conv, f.err
}

// logTo writes the observer's log in the text form, without the clock.
func logTo(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey && len(groups) == 0 {
				return slog.Attr{}
			}
			return a
		},
	}))
}

var elapsedField = regexp.MustCompile(`elapsed_ms=\d+`)

// webTurn is the origin of a turn a person started from the web.
var webTurn = valueobject.TurnOrigin{Kind: enum.TurnOriginWeb}

// turnRoom is a turn of the given origin (none when origin is empty): the owner
// line m1, a failed search, a successful search, and reply.
func turnRoom(t *testing.T, origin string, reply entity.Message) *aggregate.Conversation {
	t.Helper()
	msgs := []entity.Message{newMsg(t, "m1", enum.RoleUser, "fix the parser")}
	if origin != "" {
		msgs = append(msgs, originRecord(t, "o1", origin))
	}
	msgs = append(msgs, callRound(t, "a1", "search", "", true), callRound(t, "a2", "search", "hits", false), reply)
	return room(t, msgs...)
}

func TestObserveLogsOneLineInTheFixedOrder(t *testing.T) {
	conv := turnRoom(t, "web", finalReply(t, "a3", "done"))
	w, _ := TurnWindow(conv)
	var buf bytes.Buffer
	NewObserver(&loadOnly{conv: conv}, NewEvidence(), redact.String, logTo(&buf)).Observe("room", "send", webTurn, nil)
	got := elapsedField.ReplaceAllString(strings.TrimSpace(buf.String()), "elapsed_ms=N")
	want := fmt.Sprintf(`level=INFO msg=skill_observe conv=room kind=send signals=recovered window_msgs=3 window_runes=%d owner="fix the parser" elapsed_ms=N`, utf8.RuneCountInString(w.Digest))
	if got != want {
		t.Fatalf("line =\n%s\nwant\n%s", got, want)
	}
}

func TestObserveSkipsWhatIsNotAPersonsFinishedTurn(t *testing.T) {
	interrupted := finalReply(t, "a3", "partial")
	interrupted.SetFinishReason(enum.FinishReasonInterrupted)
	unfinished := newMsg(t, "a3", enum.RoleAssistant, "still writing")
	plain := room(t,
		newMsg(t, "m1", enum.RoleUser, "hi"),
		originRecord(t, "o1", "web"),
		finalReply(t, "a1", "hello"),
	)
	nextTurn := room(t,
		newMsg(t, "m1", enum.RoleUser, "fix the parser"),
		originRecord(t, "o1", "web"),
		callRound(t, "a1", "search", "", true),
		callRound(t, "a2", "search", "hits", false),
		finalReply(t, "a3", "done"),
		newMsg(t, "m2", enum.RoleUser, "next"),
	)
	for _, tc := range []struct {
		name   string
		conv   *aggregate.Conversation
		origin valueobject.TurnOrigin
		err    error
	}{
		{"the turn failed", turnRoom(t, "web", finalReply(t, "a3", "done")), webTurn, errors.New("boom")},
		{"automation origin", turnRoom(t, "automation", finalReply(t, "a3", "done")), valueobject.TurnOrigin{Kind: enum.TurnOriginAutomation}, nil},
		{"another thread's origin", turnRoom(t, "conversation", finalReply(t, "a3", "done")), valueobject.TurnOrigin{Kind: enum.TurnOriginConversation}, nil},
		{"no origin", turnRoom(t, "", finalReply(t, "a3", "done")), valueobject.TurnOrigin{}, nil},
		{"interrupted reply", turnRoom(t, "web", interrupted), webTurn, nil},
		{"reply still streaming", turnRoom(t, "web", unfinished), webTurn, nil},
		{"no signal", plain, webTurn, nil},
		{"a newer owner line has no reply yet", nextTurn, webTurn, nil},
	} {
		f := &loadOnly{conv: tc.conv}
		var buf bytes.Buffer
		NewObserver(f, NewEvidence(), redact.String, logTo(&buf)).Observe("room", "send", tc.origin, tc.err)
		if tc.err != nil && f.ctx != nil {
			t.Fatalf("%s: the store was read for a failed turn", tc.name)
		}
		if buf.Len() != 0 {
			t.Fatalf("%s: logged %s", tc.name, buf.String())
		}
	}
}

// An agent room writes no origin record. The origin the hub hands over decides
// the gate, so such a room's finished web turn is still observed.
func TestObserveTakesTheTurnsOriginNotARecord(t *testing.T) {
	conv := turnRoom(t, "", finalReply(t, "a3", "done"))
	var buf bytes.Buffer
	NewObserver(&loadOnly{conv: conv}, NewEvidence(), redact.String, logTo(&buf)).Observe("room", "send", webTurn, nil)
	if !strings.Contains(buf.String(), "msg=skill_observe ") {
		t.Fatalf("a web turn of a room without an origin record logged nothing: %q", buf.String())
	}
}

// The rewrite is taken only by an edit turn, and only once.
func TestObserveTakesTheRewriteOfItsOwnAnchorOnlyOnAnEdit(t *testing.T) {
	ev := NewEvidence()
	ev.RecordEdit("room", "m1", "old", nil)
	var buf bytes.Buffer
	o := NewObserver(&loadOnly{conv: turnRoom(t, "web", finalReply(t, "a3", "done"))}, ev, redact.String, logTo(&buf))
	o.Observe("room", "send", webTurn, nil)
	if strings.Contains(buf.String(), "owner_rewrote") {
		t.Fatal("a send turn took an edit record")
	}
	buf.Reset()
	o.Observe("room", "edit", webTurn, nil)
	if !strings.Contains(buf.String(), "signals=owner_rewrote,recovered") {
		t.Fatalf("edit turn did not take its rewrite: %q", buf.String())
	}
	buf.Reset()
	o.Observe("room", "edit", webTurn, nil)
	if strings.Contains(buf.String(), "owner_rewrote") {
		t.Fatal("the rewrite was taken twice")
	}
}

func TestObserveMasksTheOwnerLineBeforeItClips(t *testing.T) {
	// The key straddles the 120-rune clip. Clipping first would leave "sk-ab",
	// too short for any pattern to match, so that prefix would reach the log.
	owner := strings.Repeat("a", 114) + " sk-abcdefghijklmnop"
	conv := room(t,
		newMsg(t, "m1", enum.RoleUser, owner),
		originRecord(t, "o1", "web"),
		callRound(t, "a1", "search", "", true),
		callRound(t, "a2", "search", "hits", false),
		finalReply(t, "a3", "done"),
	)
	var buf bytes.Buffer
	NewObserver(&loadOnly{conv: conv}, NewEvidence(), redact.String, logTo(&buf)).Observe("room", "send", webTurn, nil)
	line := buf.String()
	if !strings.Contains(line, "msg=skill_observe ") {
		t.Fatalf("no line for the eligible turn: %q", line)
	}
	if strings.Contains(line, "sk-ab") || strings.Contains(line, "abcdefgh") {
		t.Fatalf("a secret prefix reached the log: %q", line)
	}
}

func TestObserveLogsALoadFailureAndSkipsTheTurn(t *testing.T) {
	var buf bytes.Buffer
	NewObserver(&loadOnly{err: errors.New("store down")}, NewEvidence(), redact.String, logTo(&buf)).Observe("room", "send", webTurn, nil)
	out := buf.String()
	if !strings.Contains(out, "msg=skill_observe_failed") || !strings.Contains(out, "store down") || strings.Contains(out, "msg=skill_observe ") {
		t.Fatalf("load failure logged %q", out)
	}
}

func TestObserveBoundsTheReadTo30Seconds(t *testing.T) {
	f := &loadOnly{conv: turnRoom(t, "web", finalReply(t, "a3", "done"))}
	var buf bytes.Buffer
	NewObserver(f, NewEvidence(), redact.String, logTo(&buf)).Observe("room", "send", webTurn, nil)
	if f.ctx == nil {
		t.Fatal("the store was not read")
	}
	deadline, ok := f.ctx.Deadline()
	if left := time.Until(deadline); !ok || left > observeTimeout || left < observeTimeout-time.Minute {
		t.Fatalf("read deadline in %v (set %v), want about %v", left, ok, observeTimeout)
	}
}
