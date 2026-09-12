# Anthropic — Prompt caching

Cache a prompt prefix so repeat requests pay ~10% of base input for it
(2.5% on Fable 5.1 / Mythos 5.1). Two ways to enable, same infrastructure:

| Mode | How | Best for |
|---|---|---|
| **Automatic** | One top-level `"cache_control": {"type": "ephemeral"}` | Growing conversations; breakpoint auto-moves to the last cacheable block |
| **Explicit** | `cache_control` on individual content blocks | Fine control (tools cached, per-request suffix not) — up to **4 breakpoints** |

Cache prefixes build in order **`tools` → `system` → `messages`**: everything up
to and including the marked block is cached. A breakpoint caches one entry —
the cumulative hash ending there — so changing anything at/before it starts a
new prefix.

## Honest cost model

- Cache **writes**: 1.25× base input (5m TTL) / 2× (1h TTL).
- Cache **reads**: 0.1× base (Fable 5.1 / Mythos 5.1: 0.025×).
- Breakpoints themselves are free; you pay only for writes/reads/uncached input.
- TTL is counted from request start — a slow 4-minute stream leaves ~1 minute
  on a 5m entry. Long thinking workflows → consider `ttl: "1h"`.

## Minimum cacheable length (per model)

| Minimum | Models |
|---|---|
| 512 tok | Fable 5.1, Mythos 5.1, Opus 5, Fable 5, Mythos 5 |
| 1,024 tok | Opus 4.8, Sonnet 5, Sonnet 4.6, Sonnet 4.5, (retired 4.x) |
| 2,048 tok | Mythos Preview, Opus 4.7 |
| 4,096 tok | Opus 4.6, Opus 4.5, Haiku 4.5 |

Below the minimum the request succeeds **uncached** (no error) — check usage
fields to detect it.

## Usage fields (verify it worked)

```json
"usage": {
  "input_tokens": 2048,
  "cache_read_input_tokens": 1800,
  "cache_creation_input_tokens": 248,
  "cache_creation": {"ephemeral_5m_input_tokens": 148, "ephemeral_1h_input_tokens": 100}
}
```

`total_input = input_tokens + cache_creation_input_tokens + cache_read_input_tokens`.
Both cache fields `0` → not cached (usually below minimum).

## Breakpoint mechanics (explicit mode)

- **Lookback window: 20 blocks.** A read checks the breakpoint position, then
  walks backwards ≤20 positions for an entry an earlier request *wrote*.
  A run of consecutive `tool_use` (or `tool_result`) blocks counts as **one**
  position, so parallel tool turns don't blow the window.
- **Writes happen only at breakpoints.** The lookback finds prior writes, not
  "stable content" — putting the breakpoint on content that changes each
  request (timestamps, the new user message) guarantees a miss.
- **Put the breakpoint on the last block identical across the requests you want
  to share.** For a varying suffix, breakpoint the end of the static prefix.
- Adding a second breakpoint closer to the old write rescues long conversations
  that outgrow the 20-block window.

## Automatic mode rules

- Uses one of the 4 breakpoint slots; combine with explicit breakpoints freely.
- No-op if the last block already has an explicit `cache_control` with the same
  TTL; **400** if TTLs differ or if all 4 explicit slots are taken.
- If the last block isn't eligible, it walks back to the nearest eligible one.
- Legacy Amazon Bedrock (Opus 4.6 and earlier): top-level `cache_control`
  returns 400 — use explicit breakpoints there.

## What can / cannot be cached

| Cacheable | Not directly cacheable |
|---|---|
| `tools` definitions, `system` blocks | `thinking` blocks (cached indirectly with their turn) |
| Text blocks in user **and** assistant turns | Citations sub-blocks (cache the top-level document) |
| Images/documents in user turns | Empty text blocks |
| `tool_use` / `tool_result` blocks | |

Invalidation cascades down the hierarchy: tool-definition changes kill
everything; system-level toggles (web search, citations) kill system + messages;
message-level edits kill from that point. Thinking/effort config changes also
invalidate (rendered into the prompt).

## Pre-warming

`max_tokens: 0` + a `cache_control` breakpoint on the shared block (typically
system prompt / tools): the API writes the cache and returns immediately
(`content: []`, `stop_reason: "max_tokens"`). Cost: a normal cache write.
Don't put the breakpoint on the placeholder user message.

## Workflow

1. Baseline one request; read usage fields (expect zeros without caching).
2. Place `cache_control` on the last stable block; re-request; confirm
   `cache_read_input_tokens > 0` on the second call.
3. Automate prefix ordering: static (tools → system → documents/examples) first,
   volatile content last.
4. For parallel requests: the entry exists only after the first response
   **begins** — warm once, then fan out.

## Edge cases

- Cache **hit ≠ guaranteed**: routing and expiry apply; treat as optimization.
- Server tools (web search) may write `ephemeral_5m` tokens you didn't request.
- Thinking + 1h TTL is a common pairing for long agent turns.
- Mid-conversation `role: "system"` messages add operator instructions without
  invalidating the cached prefix (Fable/Mythos 5.x, Opus 5/4.8; strict
  placement — must follow a user turn).
- Pre-warm requests are billed writes even with `max_tokens: 0`.

Related: [messages.md](messages.md) · [thinking.md](thinking.md) ·
[tools.md](tools.md) · [errors.md](errors.md)
