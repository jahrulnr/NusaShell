package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// failingConversations is the observer's whole view of the store. It fails
// every read, so a hook that reaches the store logs one failure line.
type failingConversations struct {
	drepo.Conversation
	loads int
}

func (f *failingConversations) Load(context.Context, string) (*aggregate.Conversation, error) {
	f.loads++
	return nil, errors.New("store down")
}

func TestWireSkillObserveOffRegistersNothing(t *testing.T) {
	hooks := 0
	edits := wireSkillObserve("off", func(func(convID, kind string, origin valueobject.TurnOrigin, external bool, err error)) { hooks++ }, nil, nil)
	if edits != nil || hooks != 0 {
		t.Fatalf("off: edit evidence %v and %d finished hooks; want neither", edits, hooks)
	}
}

func TestWireSkillObserveRegistersOneHookThatLogsOnce(t *testing.T) {
	var hooks []func(convID, kind string, origin valueobject.TurnOrigin, external bool, err error)
	store := &failingConversations{}
	var buf bytes.Buffer
	edits := wireSkillObserve("observe", func(fn func(convID, kind string, origin valueobject.TurnOrigin, external bool, err error)) {
		hooks = append(hooks, fn)
	}, store, slog.New(slog.NewTextHandler(&buf, nil)))
	if edits == nil || len(hooks) != 1 {
		t.Fatalf("observe: edit evidence %v and %d finished hooks; want evidence and one hook", edits, len(hooks))
	}
	hooks[0]("room", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginWeb}, false, nil)
	if store.loads != 1 {
		t.Fatalf("the hook read the room %d times, want once", store.loads)
	}
	if out := buf.String(); strings.Count(out, "\n") != 1 || !strings.Contains(out, "msg=skill_observe_failed conv=room") {
		t.Fatalf("the hook logged %q, want one skill_observe_failed line", out)
	}
}
