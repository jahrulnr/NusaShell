package application

import (
	"sort"
	"sync"

	"nusashell/domain"
)

// trackPendingRun records that a background run is active for a
// conversation, together with the tool that spawned it (used by the
// hydration slot so a fresh context knows which background agents are
// still running). Called when an async tool spawns a run.
func (a *App) trackPendingRun(conversationID, runID, tool string) {
	if conversationID == "" || runID == "" {
		return
	}
	a.pendingRunsMu.Lock()
	defer a.pendingRunsMu.Unlock()
	if a.pendingRuns == nil {
		a.pendingRuns = map[string]map[string]string{}
	}
	if a.pendingRuns[conversationID] == nil {
		a.pendingRuns[conversationID] = map[string]string{}
	}
	a.pendingRuns[conversationID][runID] = tool
}

// untrackPendingRun removes a completed background run. Returns true
// if the run was found and removed, false if it was not tracked.
func (a *App) untrackPendingRun(conversationID, runID string) bool {
	a.pendingRunsMu.Lock()
	defer a.pendingRunsMu.Unlock()
	set := a.pendingRuns[conversationID]
	if set == nil {
		return false
	}
	if _, ok := set[runID]; !ok {
		return false
	}
	delete(set, runID)
	if len(set) == 0 {
		delete(a.pendingRuns, conversationID)
	}
	return true
}

// hasPendingRuns reports whether any background runs are still active
// for the given conversation. Used by the auto-continue policy to decide
// whether to pause (awaiting-background-jobs) or proceed.
func (a *App) hasPendingRuns(conversationID string) bool {
	a.pendingRunsMu.Lock()
	defer a.pendingRunsMu.Unlock()
	return len(a.pendingRuns[conversationID]) > 0
}

// pendingBackgroundRuns snapshots the active background runs for a
// conversation as the hydration slot payload: run ID, spawning tool, and
// worker detail when known (internal delegates carry an AcpRun). Returns a
// deterministic (ID-sorted) list, empty when no runs are pending. The
// parent conversation reads this after compaction so it still knows which
// background agents were spawned and are running.
func (a *App) pendingBackgroundRuns(conversationID string) []domain.BackgroundRunInfo {
	a.pendingRunsMu.Lock()
	set := a.pendingRuns[conversationID]
	ids := make([]string, 0, len(set))
	for runID := range set {
		ids = append(ids, runID)
	}
	a.pendingRunsMu.Unlock()
	sort.Strings(ids)

	out := make([]domain.BackgroundRunInfo, 0, len(ids))
	for _, runID := range ids {
		a.pendingRunsMu.Lock()
		tool := set[runID]
		a.pendingRunsMu.Unlock()
		info := domain.BackgroundRunInfo{
			ID:     runID,
			Tool:   tool,
			Status: "pending",
		}
		a.delegateRunsMu.RLock()
		if dr := a.delegateRuns[runID]; dr != nil {
			info.Agent = dr.AgentName
			info.Model = dr.CurrentModelID
			info.Workspace = dr.Workspace
		}
		a.delegateRunsMu.RUnlock()
		out = append(out, info)
	}
	return out
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
