package application

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// ReviewTranscript is the persisted record of one background review run:
// the review agent's own conversation with the LLM (user transcript →
// assistant tool_calls → tool results → …). Stored as one JSON file per
// review under <dataDir>/learning/reviews/<id>.json.
type ReviewTranscript struct {
	ID             string        `json:"id"`
	ConversationID string        `json:"conversation_id"`
	Model          string        `json:"model"`
	CreatedAt      time.Time     `json:"created_at"`
	Messages       []ChatMessage `json:"messages"`
}

// reviewTranscriptDir is the folder under the data directory that holds
// review transcript files.
func reviewTranscriptDir(dataDir string) string {
	return filepath.Join(dataDir, "learning", "reviews")
}

// saveReviewTranscript writes the review agent's message history to a
// JSON file so the learning log can surface it later. Returns the review
// ID (timestamp-based, unique per run) or an empty string if the data
// directory is not configured or the write fails (best-effort — the
// review still completed, just without a viewable transcript).
func saveReviewTranscript(dataDir, conversationID, model string, messages []ChatMessage) string {
	if dataDir == "" || len(messages) == 0 {
		return ""
	}
	id := time.Now().UTC().Format("20060102T150405Z") + "_" + safeID(conversationID)
	dir := reviewTranscriptDir(dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	t := ReviewTranscript{
		ID:             id,
		ConversationID: conversationID,
		Model:          model,
		CreatedAt:      time.Now().UTC(),
		Messages:       messages,
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return ""
	}
	path := filepath.Join(dir, id+".json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return ""
	}
	return id
}

// ReadReviewTranscript loads a review transcript by ID. Returns nil if the
// file is missing or unreadable — the log view must never fail because a
// transcript was pruned.
func ReadReviewTranscript(dataDir, id string) *ReviewTranscript {
	if dataDir == "" || id == "" {
		return nil
	}
	path := filepath.Join(reviewTranscriptDir(dataDir), id+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var t ReviewTranscript
	if json.Unmarshal(b, &t) != nil {
		return nil
	}
	return &t
}

// safeID strips non-alphanumeric characters from a conversation ID so the
// transcript filename stays filesystem-safe.
func safeID(s string) string {
	out := make([]byte, 0, len(s))
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			out = append(out, byte(c))
		}
	}
	if len(out) == 0 {
		return "anon"
	}
	return string(out)
}
