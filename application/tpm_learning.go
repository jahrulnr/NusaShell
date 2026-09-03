package application

import (
	"nusashell/domain"
)

// tpmContextCap derives a safe context-window cap from a tokens-per-minute
// budget. A request should cost roughly half the per-minute window so it
// fits even while the window is partially consumed by concurrent traffic;
// the completion budget the request will also consume is subtracted. Floored
// at a quarter of the window so an extreme output budget cannot starve the
// context down to nothing.
func tpmContextCap(limit, maxOutput int) int {
	if limit <= 0 {
		return 0
	}
	cap := limit/2 - maxOutput
	if floor := limit / 4; cap < floor {
		cap = floor
	}
	if cap < 1 {
		cap = 1
	}
	return cap
}

// tpmRejection carries the provider's own per-minute token accounting:
// limit = per-minute budget, used = already consumed this window, requested =
// what the rejected request needs.
type tpmRejection struct {
	limit, used, requested int
}

// parseTPMRejection extracts the per-minute accounting from a provider error.
// Only official OpenAI bodies match ("tokens per min (TPM): Limit N, Used N,
// Requested N"); other providers' rate-limit text never parses, so the
// behavior stays OpenAI-specific by construction.
func parseTPMRejection(err error) (tpmRejection, bool) {
	limit, used, requested, ok := domain.ParseTPMError(extractErrBody(err))
	return tpmRejection{limit: limit, used: used, requested: requested}, ok
}

// isTPMDominatedRequest reports whether a TPM rejection was caused by the
// request itself demanding more than half the per-minute budget (as opposed
// to plain congestion from other traffic). Waiting helps only the latter:
// a dominant request keeps failing every retry until the window is nearly
// idle. The durable fix is shrinking the request — the emergency-compaction
// hook turns this into a compact-then-retry, and learnTPMContextCap teaches
// every conversation on this provider+model to compact earlier.
func isTPMDominatedRequest(err error) bool {
	tpm, ok := parseTPMRejection(err)
	return ok && tpm.requested*2 > tpm.limit
}

// learnTPMContextCap records a context-window cap derived from a dominant
// TPM rejection for the run's provider+model. resolveContextWindow consults
// the learned cap, so compaction triggers earlier in every conversation
// using that provider+model — future requests stay within the per-minute
// budget instead of spinning through provider retries. Returns true when a
// cap was newly learned. Best-effort: nil learnedParams or an absent
// provider key simply skips learning.
func (a *App) learnTPMContextCap(run *TurnRun, model string, err error, maxOutput int) bool {
	if a.learnedParams == nil || run == nil || run.ProviderID == "" {
		return false
	}
	tpm, ok := parseTPMRejection(err)
	if !ok || tpm.requested*2 <= tpm.limit {
		return false // modest request; congestion, not size — waiting is the fix
	}
	cap := tpmContextCap(tpm.limit, maxOutput)
	if !a.learnedParams.LearnTPMContextCap(run.ProviderID, model, cap, extractErrBody(err)) {
		return false
	}
	a.log("info", "learning", "learned TPM context cap for %s/%s: %d tokens (limit %d/min, %d used, this request wanted %d)",
		a.providerNameByID(run.ProviderID), model, cap, tpm.limit, tpm.used, tpm.requested)
	return true
}
