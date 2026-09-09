// Package codex is a Go port of the Codex (codex-rs) wire contract for the
// ChatGPT backend `/responses` and standalone `alpha/search` APIs, focused on
// four concerns:
//
//   - Context calculation: coarse context-token accounting over ResponseItem
//     history plus the token_limit_reached decision used to trigger
//     auto-compaction (ported from core/src/session/context_window.rs,
//     core/src/context_manager/history.rs, and core/src/state/auto_compact_window.rs).
//   - Responses handle: the ResponsesApiRequest wire shape, the tagged
//     ResponseItem union (including opaque encrypted_content passthrough and
//     the request-only compaction_trigger control item), and the SSE event
//     stream decoder (ported from protocol/src/models.rs,
//     codex-api/src/common.rs, and codex-api/src/sse/responses.rs).
//   - Compaction handle: remote v2 compaction, which streams a normal
//     POST /responses request whose input ends with {"type":"compaction_trigger"}
//     and expects exactly one opaque compaction output item before
//     response.completed, then rebuilds the retained history (ported from
//     core/src/compact_remote_v2.rs and core/src/compact_remote_v2_attempt.rs).
//   - Standalone web search: a bounded query-only POST /alpha/search client
//     whose auth, account selection, and cookie jar are supplied by the outer
//     application adapter.
//
// The package also owns the thin core.Provider transport for the Responses
// endpoint:
// it converts core blocks to Responses items, applies Codex authentication
// headers supplied by the caller, decodes the SSE stream, and exposes opaque
// compaction items through core.Response. OAuth refresh and account routing
// remain outside the package.
//
// Out of scope (by decision): image generation (t2i/i2i), audio content
// estimation, multi-agent AgentMessage items, WebSocket transport fallback,
// and window UUIDs used for rollout persistence.
//
// encrypted_content values are provider-owned opaque state: they are copied
// byte-for-byte through storage and the next request, never decoded,
// truncated, logged, or synthesized.
package codex
