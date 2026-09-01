package application

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// turnCountersPath returns the path to the persisted turn counter file.
func (a *App) turnCountersPath() string {
	if a.DataDir == "" {
		return ""
	}
	return filepath.Join(a.DataDir, "learning", "turns.json")
}

// loadTurnCounters restores per-conversation review counters from disk so
// that the learning review thresholds survive server restarts. Without
// this, a user who restarts frequently never reaches the threshold and
// the review agent never fires. The file stores both turn counters and
// tool-call counters; the legacy flat map[string]int format (turns only)
// is migrated on load.
func (a *App) loadTurnCounters(dataDir string) {
	path := filepath.Join(dataDir, "learning", "turns.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return // file doesn't exist yet — fresh start
	}
	// Try the new struct format first.
	var persisted struct {
		Turns     map[string]int `json:"turns"`
		ToolCalls map[string]int `json:"tool_calls"`
	}
	if err := json.Unmarshal(data, &persisted); err == nil && (persisted.Turns != nil || persisted.ToolCalls != nil) {
		a.learningMu.Lock()
		if persisted.Turns != nil {
			a.turnsSinceReview = persisted.Turns
		}
		if persisted.ToolCalls != nil {
			a.toolCallsSinceReview = persisted.ToolCalls
		}
		a.learningMu.Unlock()
		a.log("info", "learning", "loaded %d turn + %d tool-call counter(s) from disk", len(persisted.Turns), len(persisted.ToolCalls))
		return
	}
	// Legacy flat map[string]int format (turns only).
	var counters map[string]int
	if err := json.Unmarshal(data, &counters); err != nil {
		return
	}
	a.learningMu.Lock()
	a.turnsSinceReview = counters
	a.learningMu.Unlock()
	if len(counters) > 0 {
		a.log("info", "learning", "migrated %d legacy turn counter(s) from disk", len(counters))
	}
}

// saveTurnCounters persists turn and tool-call counters to disk. Called on
// lifecycle shutdown and after each counter update.
func (a *App) saveTurnCounters() {
	path := a.turnCountersPath()
	if path == "" {
		return
	}
	a.learningMu.RLock()
	turns := make(map[string]int, len(a.turnsSinceReview))
	for k, v := range a.turnsSinceReview {
		turns[k] = v
	}
	toolCalls := make(map[string]int, len(a.toolCallsSinceReview))
	for k, v := range a.toolCallsSinceReview {
		toolCalls[k] = v
	}
	a.learningMu.RUnlock()
	if len(turns) == 0 && len(toolCalls) == 0 {
		return
	}
	persisted := struct {
		Turns     map[string]int `json:"turns"`
		ToolCalls map[string]int `json:"tool_calls"`
	}{Turns: turns, ToolCalls: toolCalls}
	data, err := json.Marshal(persisted)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, data, 0o600)
}
