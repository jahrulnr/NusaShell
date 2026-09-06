// Package codex is a Go port of the Codex (codex-rs) wire contract for the
// ChatGPT backend `/responses` API, focused on three concerns:
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
//     The legacy unary POST /responses/compact DTO is also modeled for wire
//     fidelity, but only the v2 flow is driven here.
//
// Ported wire packages under infrastructure/ai/ stay structurally close to
// upstream and import only infrastructure/ai/core, never application or
// domain. HTTP transport, auth headers, and provider wiring are owned by the
// adapter layer and are intentionally not part of this package.
//
// Out of scope (by decision): image generation (t2i/i2i), audio content
// estimation, multi-agent AgentMessage items, WebSocket transport fallback,
// and window UUIDs used for rollout persistence.
//
// encrypted_content values are provider-owned opaque state: they are copied
// byte-for-byte through storage and the next request, never decoded,
// truncated, logged, or synthesized.
package codex
