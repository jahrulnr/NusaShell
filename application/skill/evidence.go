package skill

import (
	"sync"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
)

// EditRecord is one owner rewrite: the message it replaced, the text that
// message held before, and the messages the rewrite dropped.
type EditRecord struct {
	MsgID    string
	Previous string
	Dropped  []entity.Message
}

// Evidence keeps each room's latest owner rewrite until the finished turn that
// opened at the replaced message takes it. It lives in process memory: a
// restart drops it, and a record no turn takes is replaced by the room's next
// edit.
type Evidence struct {
	mu    sync.Mutex
	rooms map[string]EditRecord
}

func NewEvidence() *Evidence { return &Evidence{rooms: map[string]EditRecord{}} }

// RecordEdit implements job.EditEvidence. The store takes ownership of tail.
func (e *Evidence) RecordEdit(convID, msgID, previous string, tail []entity.Message) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rooms[convID] = EditRecord{MsgID: msgID, Previous: previous, Dropped: tail}
}

// DiscardEdit implements job.EditEvidence: a failed edit's rewrite is forgotten.
func (e *Evidence) DiscardEdit(convID, msgID string) { e.Take(convID, msgID) }

// Take removes and returns the room's rewrite when it replaced msgID, the
// message that opened the turn being observed. Any other rewrite stays.
func (e *Evidence) Take(convID, msgID string) (EditRecord, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	rec, ok := e.rooms[convID]
	if !ok || rec.MsgID != msgID {
		return EditRecord{}, false
	}
	delete(e.rooms, convID)
	return rec, true
}
