package valueobject

import "time"

// Completion is one asynchronous finish a room has not read yet — an exit a
// producer reported outside any turn. It is data: a status line and a bounded
// detail, never words an owner wrote.
type Completion struct {
	// Source names the producer kind: "session" for a terminal session.
	Source string `json:"source"`
	// ID is the producer's handle inside that source — a session id.
	ID string `json:"id"`
	// Status says how it ended in one word a model can read ("exited", "failed").
	Status string `json:"status"`
	// Detail is the bounded human part: the exit code and the tail of output.
	Detail string `json:"detail,omitempty"`
	// At is when the producer reported it. The sidecar keeps arrival order, so a
	// process that restarts reads what waited in the order it happened.
	At time.Time `json:"at"`
}

// Key is the value that identifies one finish: a producer reporting the same
// exit twice is one record, not two.
func (c Completion) Key() string { return c.Source + "\x00" + c.ID }
