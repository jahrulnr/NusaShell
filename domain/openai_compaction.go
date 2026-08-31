package domain

import (
	"strings"
)

// serverCompactionMinContextWindow is the minimum context window for a model
// to use server-side compaction via context_management. Models below this
// threshold (e.g. gpt-4o at 128k) stay on the client-side summarization path
// because the small window leaves too little headroom for the server to
// compact effectively before hitting the hard limit.
const serverCompactionMinContextWindow = 200_000

// modelLookupKey normalizes a model ID for table lookup: strips the
// "openai/" prefix, lowercases, and removes dots and dashes so that
// "openai/gpt-5.6-luna", "GPT-5.6-LUNA", and "gpt56luna" all resolve to the
// same key. This mirrors the normalization in openai-agents-python's
// compaction capability.
func modelLookupKey(model string) string {
	s := strings.TrimSpace(strings.ToLower(model))
	s = strings.TrimPrefix(s, "openai/")
	return strings.NewReplacer(".", "", "-", "").Replace(s)
}

// serverCompactionWindows maps normalized model IDs to their context window
// sizes. Only models in this table are eligible for server-side compaction.
// Source: openai-agents-python _MODEL_CONTEXT_WINDOWS (the official reference
// implementation).
var serverCompactionWindows = map[string]int{}

// init builds the table from the model groups below, keeping the data
// declarative and easy to audit against upstream.
func init() {
	register := func(window int, models ...string) {
		for _, m := range models {
			serverCompactionWindows[modelLookupKey(m)] = window
		}
	}

	// 1,047,576 context window
	register(1_047_576,
		"gpt-5.4", "gpt-5.4-2026-03-05", "gpt-5.4-pro", "gpt-5.4-pro-2026-03-05",
		"gpt-5.5", "gpt-5.5-2026-04-23", "gpt-5.5-pro", "gpt-5.5-pro-2026-04-23",
		"gpt-5.6", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna",
		"gpt-4.1", "gpt-4.1-2025-04-14", "gpt-4.1-mini", "gpt-4.1-mini-2025-04-14",
		"gpt-4.1-nano", "gpt-4.1-nano-2025-04-14",
	)

	// 400,000 context window
	register(400_000,
		"gpt-5", "gpt-5-2025-08-07", "gpt-5-codex", "gpt-5-mini", "gpt-5-mini-2025-08-07",
		"gpt-5-nano", "gpt-5-nano-2025-08-07", "gpt-5-pro", "gpt-5-pro-2025-10-06",
		"gpt-5.1", "gpt-5.1-2025-11-13", "gpt-5.1-codex", "gpt-5.1-codex-max",
		"gpt-5.1-codex-mini", "gpt-5.2", "gpt-5.2-2025-12-11", "gpt-5.2-codex",
		"gpt-5.2-pro", "gpt-5.2-pro-2025-12-11", "gpt-5.3-codex",
		"gpt-5.4-mini", "gpt-5.4-mini-2026-03-17", "gpt-5.4-nano", "gpt-5.4-nano-2026-03-17",
	)

	// 200,000 context window
	register(200_000,
		"codex-mini-latest", "o1", "o1-2024-12-17", "o1-pro", "o1-pro-2025-03-19",
		"o3", "o3-2025-04-16", "o3-deep-research", "o3-deep-research-2025-06-26",
		"o3-mini", "o3-mini-2025-01-31", "o3-pro", "o3-pro-2025-06-10",
		"o4-mini", "o4-mini-2025-04-16", "o4-mini-deep-research",
		"o4-mini-deep-research-2025-06-26",
	)

	// 128,000 context window — below the 200k floor, so these models are NOT
	// eligible for server-side compaction despite being in the table. They
	// are registered so OpenAIServerCompactionContextWindow can return the
	// value for diagnostics, but OpenAISupportsServerCompaction returns false.
	register(128_000,
		"gpt-4o", "gpt-4o-2024-05-13", "gpt-4o-2024-08-06", "gpt-4o-2024-11-20",
		"gpt-4o-mini", "gpt-4o-mini-2024-07-18",
		"gpt-5-chat-latest", "gpt-5.1-chat-latest", "gpt-5.2-chat-latest",
		"gpt-5.3-chat-latest",
	)
}

// OpenAISupportsServerCompaction reports whether a model ID is eligible for
// server-side compaction via the context_management parameter in
// POST /responses. The model must be in the official supported table AND have
// a context window >= serverCompactionMinContextWindow (200k). Models below
// the floor (gpt-4o at 128k) stay on client-side summarization because the
// small window leaves insufficient headroom for effective server-side
// compaction.
func OpenAISupportsServerCompaction(modelID string) bool {
	window, ok := serverCompactionWindows[modelLookupKey(modelID)]
	if !ok {
		return false
	}
	return window >= serverCompactionMinContextWindow
}

// OpenAIServerCompactionContextWindow returns the context window size used
// for server-side compaction threshold calculation. Returns 0 for models not
// in the supported table. For models below the 200k floor, the value is
// returned (for diagnostics) but OpenAISupportsServerCompaction returns false.
func OpenAIServerCompactionContextWindow(modelID string) int {
	return serverCompactionWindows[modelLookupKey(modelID)]
}

// ServerCompactionThresholdFloor is the minimum compact_threshold we ever
// send. It matches the client-side compaction trigger so the server does not
// wait longer than the client would have. Models with larger context windows
// get a higher threshold (90% of window), but never below this floor.
var ServerCompactionThresholdFloor = 120_000

// ServerCompactionThreshold returns the compact_threshold value for a model
// with the given context window: max(window*0.9, floor). Small-window
// eligible models (200k) trigger at a reasonable point while large-window
// models (400k–1M) use most of their window before compacting.
func ServerCompactionThreshold(window int) int {
	threshold := int(float64(window) * 0.9)
	if threshold < ServerCompactionThresholdFloor {
		threshold = ServerCompactionThresholdFloor
	}
	return threshold
}
