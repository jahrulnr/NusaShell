package repository

import (
	"context"
	"encoding/json"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// Browser session recordings: a timeline of what happened in a thread's
// browser tab, its network lines, a summary, and artifacts (downloads).

// RecordingCounts tallies the kinds the summary reports on.
type RecordingCounts struct {
	Events      int64 `json:"events"`
	Annotations int64 `json:"annotations"`
	Errors      int64 `json:"errors"`
}

// RecMeta is the meta.json snapshot — rewritten atomically on every status
// change and refreshed by the writer goroutine about once a second.
type RecordingMeta struct {
	ID                string          `json:"id"`
	SessionID         string          `json:"sessionId"`
	ConversationID    string          `json:"conversationId"`
	Profile           string          `json:"profile"`
	Status            string          `json:"status"`
	Env               string          `json:"env"`
	CreatedAt         int64           `json:"createdAt"`
	StartedAt         int64           `json:"startedAt"`
	EndedAt           int64           `json:"endedAt,omitempty"`
	LastSeq           int64           `json:"lastSeq"`
	Counts            RecordingCounts `json:"counts"`
	InterruptedReason string          `json:"interruptedReason,omitempty"`
	Truncated         bool            `json:"truncated,omitempty"`
}

// RecEvent is one events.jsonl line. Seq is assigned by the writer goroutine,
// so event order is append order, not wire order.
type RecordingEvent struct {
	Seq    int64           `json:"seq"`
	TS     int64           `json:"ts"` // unix ms, assigned at append time
	Origin string          `json:"origin"`
	Type   string          `json:"type"`
	Data   json.RawMessage `json:"data"`
}

// RecordingReader reads one recording, live or finished.
type RecordingReader interface {
	Meta() RecordingMeta
	// Events pages the timeline after a seq, optionally only some types;
	// it returns the next cursor and whether the end was reached.
	Events(after int64, limit int, types []string) ([]RecordingEvent, int64, bool)
	Network(after int64, limit int) ([]json.RawMessage, int64, bool)
	// Summary is the finished recording's summary text.
	Summary() (string, error)
	// Artifact reads a file of the recording (downloads/<guid> for a
	// download) and its media type.
	Artifact(name string) ([]byte, string, error)
}

// RecordingWriter is a live recording.
type RecordingWriter interface {
	dservice.RecordingSink
	RecordingReader
	Pause() error
	Resume() error
	// Stop finalizes it, summary included.
	Stop() (*RecordingMeta, error)
}

// Recordings stores recordings.
type Recordings interface {
	// Start opens a recording; mask hides secrets in what is written.
	Start(sessionID, conversationID, profile, env string, mask func(string) string) (RecordingWriter, error)
	Get(id string) (RecordingReader, error)
	// List returns a conversation's recordings ("" for all), newest first.
	List(conversationID string) []RecordingMeta
	// Purge finalizes every live recorder, then removes all recordings
	// — called only once the threads that made them are gone.
	Purge(ctx context.Context) error
}
