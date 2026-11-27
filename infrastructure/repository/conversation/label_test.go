package repoconversation

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

func unsavedRoom(t *testing.T) *aggregate.Conversation {
	t.Helper()
	conv, err := aggregate.NewConversation("")
	if err != nil {
		t.Fatal(err)
	}
	return conv
}

// A room's handle is unique because the store allocates it in the same
// critical section that writes the record: creating rooms in parallel is the
// path that used to hand two rooms the same animal, since the caller read the
// set of used labels before either room had written its own.
func TestConcurrentCreatesGetDistinctLabels(t *testing.T) {
	repo := NewConversationRepository(t.TempDir())
	const rooms = 12
	convs := make([]*aggregate.Conversation, rooms)
	errs := make([]error, rooms)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range rooms {
		convs[i] = unsavedRoom(t)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = repo.Save(context.Background(), convs[i])
		}(i)
	}
	close(start)
	wg.Wait()

	owner := make(map[string]string, rooms)
	for i, conv := range convs {
		if errs[i] != nil {
			t.Fatalf("room %d: %v", i, errs[i])
		}
		label := conv.Label()
		if label == "" {
			t.Fatalf("room %s was created without a handle", conv.ID())
		}
		if other, taken := owner[label]; taken {
			t.Fatalf("rooms %s and %s share @%s", other, conv.ID(), label)
		}
		owner[label] = conv.ID()
	}
}

// Save is the rename write too: a handle another room holds is refused under
// the same lock, so two renames racing for one name cannot both land.
func TestSaveRefusesALabelAnotherRoomOwns(t *testing.T) {
	repo := NewConversationRepository(t.TempDir())
	first := unsavedRoom(t)
	if err := repo.Save(context.Background(), first); err != nil {
		t.Fatal(err)
	}

	second := unsavedRoom(t)
	if err := second.SetLabel(first.Label()); err != nil {
		t.Fatal(err)
	}
	err := repo.Save(context.Background(), second)
	if code := apperror.As(err).StatusCode(); code != 409 {
		t.Fatalf("duplicate handle: error %v (status %d), want 409", err, code)
	}
	if _, err := repo.Info(context.Background(), second.ID()); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("refused save left a record behind: %v", err)
	}
}

// Every turn saves the room again; a room keeping its own handle must not be
// read as a conflict with itself.
func TestSaveKeepsItsOwnLabel(t *testing.T) {
	repo := NewConversationRepository(t.TempDir())
	conv := unsavedRoom(t)
	if err := repo.Save(context.Background(), conv); err != nil {
		t.Fatal(err)
	}
	label := conv.Label()

	stored, err := repo.Load(context.Background(), conv.ID())
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if err := repo.Save(context.Background(), stored); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	if got := stored.Label(); got != label {
		t.Fatalf("handle changed on update: %q, want %q", got, label)
	}
}

// A crash between a clear's two renames parks the live room. Its handle is
// still theirs: the next room must not be given it, and putting the parked
// room back must not rename it. Otherwise two rooms share one @label.
func TestParkedRoomKeepsItsLabelAndANewRoomDoesNotTakeIt(t *testing.T) {
	repo := NewConversationRepository(t.TempDir())
	var held *aggregate.Conversation
	for range 8 {
		conv := unsavedRoom(t)
		if err := repo.Save(context.Background(), conv); err != nil {
			t.Fatal(err)
		}
		if conv.Label() == "eagle" {
			held = conv
		}
	}
	if held == nil {
		t.Fatal("the eighth room should have been given eagle")
	}
	parked := filepath.Join(repo.path, stagingRoot, held.ID()+parkedMark+"1")
	if err := os.MkdirAll(filepath.Dir(parked), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(repo.convDir(held.ID()), parked); err != nil {
		t.Fatal(err)
	}

	next := unsavedRoom(t)
	if err := repo.Save(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	infos, err := repo.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	owners := map[string]string{}
	for _, info := range infos {
		if info.Label == "" {
			continue
		}
		if other, taken := owners[info.Label]; taken {
			t.Fatalf("rooms %s and %s share @%s", other, info.ID, info.Label)
		}
		owners[info.Label] = info.ID
	}
	if owners["eagle"] != held.ID() {
		t.Fatalf("@eagle belongs to %s, want the parked room %s", owners["eagle"], held.ID())
	}
	if next.Label() == "eagle" {
		t.Fatal("the new room was given the parked room's label")
	}
}
