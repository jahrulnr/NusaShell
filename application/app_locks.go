package application

import (
	"sync"
)

// trackPendingRun records that a background run is active for a
// conversation. Called when an async tool spawns a run (subagent today).
func (a *App) trackPendingRun(conversationID, runID string) {
	if conversationID == "" || runID == "" {
		return
	}
	a.pendingRunsMu.Lock()
	defer a.pendingRunsMu.Unlock()
	if a.pendingRuns == nil {
		a.pendingRuns = map[string]map[string]bool{}
	}
	if a.pendingRuns[conversationID] == nil {
		a.pendingRuns[conversationID] = map[string]bool{}
	}
	a.pendingRuns[conversationID][runID] = true
}

// untrackPendingRun removes a completed subagent run. Returns true
// if the run was found and removed, false if it was not tracked.
func (a *App) untrackPendingRun(conversationID, runID string) bool {
	a.pendingRunsMu.Lock()
	defer a.pendingRunsMu.Unlock()
	set := a.pendingRuns[conversationID]
	if set == nil {
		return false
	}
	if !set[runID] {
		return false
	}
	delete(set, runID)
	if len(set) == 0 {
		delete(a.pendingRuns, conversationID)
	}
	return true
}

// hasPendingRuns reports whether any subagent runs are still active
// for the given conversation. Used by the auto-continue policy to decide
// whether to pause (awaiting-background-jobs) or proceed.
func (a *App) hasPendingRuns(conversationID string) bool {
	a.pendingRunsMu.Lock()
	defer a.pendingRunsMu.Unlock()
	return len(a.pendingRuns[conversationID]) > 0
}

// rootMutationLock returns a mutex serializing mutating tool calls against
// the same workspace root. Roots are keyed by absolute path; the registry
// is lazy and lives for the process lifetime.
func (a *App) rootMutationLock(root string) *sync.Mutex {
	a.journalRootsMu.Lock()
	defer a.journalRootsMu.Unlock()
	if a.journalRoots == nil {
		a.journalRoots = map[string]*sync.Mutex{}
	}
	mu, ok := a.journalRoots[root]
	if !ok {
		mu = &sync.Mutex{}
		a.journalRoots[root] = mu
	}
	return mu
}

func (a *App) conversationTurnLock(conversationID string) *sync.Mutex {
	a.conversationTurnsMu.Lock()
	defer a.conversationTurnsMu.Unlock()
	if a.conversationTurns == nil {
		a.conversationTurns = map[string]*sync.Mutex{}
	}
	lock, ok := a.conversationTurns[conversationID]
	if !ok {
		lock = &sync.Mutex{}
		a.conversationTurns[conversationID] = lock
	}
	return lock
}

// journalArchiver is the optional lifecycle extension a ChangeJournal may
// implement: compress the live journal at turn end so the JSONL stays
// bounded. Kept as a narrow type assertion (same pattern as the streaming
// toolbox) so the ChangeJournal port stays minimal.
type journalArchiver interface {
	Archive(conversationID string) error
}

// journalRemover is the optional lifecycle extension for deleting a
// conversation's journal sidecar when the conversation is deleted.
type journalRemover interface {
	Remove(conversationID string) error
}

// archiveJournal compresses the conversation's live journal after a turn
// ends. Failures are logged, never surfaced: journaling must not break the
// agent loop.
func (a *App) archiveJournal(conversationID string) {
	archiver, ok := a.Journal.(journalArchiver)
	if !ok {
		return
	}
	if err := archiver.Archive(conversationID); err != nil {
		a.log("warn", "journal", "archive failed for %s: %v", conversationID, err)
	}
}

// removeJournal deletes the conversation's journal sidecar. Called from the
// conversation delete handler after the conversation record is gone.
func (a *App) removeJournal(conversationID string) {
	remover, ok := a.Journal.(journalRemover)
	if !ok {
		return
	}
	if err := remover.Remove(conversationID); err != nil {
		a.log("warn", "journal", "remove failed for %s: %v", conversationID, err)
	}
}
