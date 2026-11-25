package appconversation

import (
	"context"
	"fmt"
	"sync"
	"testing"

	appjob "github.com/jahrulnr/nusashell/application/job"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// staleListRepo answers List with an empty room set forever. It stands in for
// the read-then-write window the old allocation had: two rooms created at the
// same moment both read the labels in use before either room was written, so
// both picked the same animal. Whatever the caller reads here, the store owns
// the room records and must still hand each room its own handle.
type staleListRepo struct {
	drepo.Conversation
}

func (r *staleListRepo) List(context.Context) ([]drepo.ConversationInfo, error) {
	return nil, nil
}

func newRoomIn(t *testing.T, app *ConversationApp, text string) string {
	t.Helper()
	res, err := app.Send(t.Context(), "", "gw1", "m1", text, nil, appjob.SendOptions{}, nil)
	if err != nil {
		t.Fatalf("send %q: %v", text, err)
	}
	return res.ConversationID
}

// Two rooms created at the same moment must not share a handle: a chat that
// types @<handle> has to name exactly one room.
func TestConcurrentRoomCreationGetsDistinctLabels(t *testing.T) {
	app, _ := fixtureWith(t, okHandler, func(c drepo.Conversation) drepo.Conversation {
		return &staleListRepo{Conversation: c}
	})

	const rooms = 4
	ids := make([]string, rooms)
	errs := make([]error, rooms)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < rooms; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res, err := app.Send(context.Background(), "", "gw1", "m1", fmt.Sprintf("room %d", i), nil, appjob.SendOptions{}, nil)
			ids[i], errs[i] = res.ConversationID, err
		}(i)
	}
	close(start)
	wg.Wait()

	owner := make(map[string]string, rooms)
	for i := 0; i < rooms; i++ {
		if errs[i] != nil {
			t.Fatalf("room %d: %v", i, errs[i])
		}
		info, err := app.Info(t.Context(), ids[i])
		if err != nil {
			t.Fatal(err)
		}
		if info.Label == "" {
			t.Fatalf("room %s was created without a handle", ids[i])
		}
		if other, taken := owner[info.Label]; taken {
			t.Fatalf("rooms %s and %s share @%s", other, ids[i], info.Label)
		}
		owner[info.Label] = ids[i]
	}
}

// Renaming two rooms to one handle is the same race through another door: one
// rename takes the handle and the other is refused, never both.
func TestConcurrentRenamesToOneLabelLeaveOneOwner(t *testing.T) {
	app, _ := fixtureWith(t, okHandler, func(c drepo.Conversation) drepo.Conversation {
		return &staleListRepo{Conversation: c}
	})
	first := newRoomIn(t, app, "first")
	second := newRoomIn(t, app, "second")

	const handle = "otter"
	errs := make([]error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, id := range []string{first, second} {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			<-start
			label := handle
			_, errs[i] = app.Annotate(context.Background(), id, Annotation{Label: &label})
		}(i, id)
	}
	close(start)
	wg.Wait()

	ok, conflict := 0, 0
	for i, err := range errs {
		switch code := apperror.As(err).StatusCode(); {
		case err == nil:
			ok++
		case code == 409:
			conflict++
		default:
			t.Fatalf("rename %d: %v", i, err)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("one rename must win and the other be refused: %d won, %d refused", ok, conflict)
	}

	owners := 0
	for _, id := range []string{first, second} {
		info, err := app.Info(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if info.Label == handle {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("%d rooms hold @%s, want 1", owners, handle)
	}
}
