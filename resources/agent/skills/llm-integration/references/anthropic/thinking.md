# Anthropic — Thinking, effort, signatures

Claude "thinking" replaces the old manual budget on current models:
**adaptive thinking + `output_config.effort`** decide when and how deeply the
model reasons. Thinking arrives as `thinking` content blocks ahead of `text`,
each with an opaque `signature` you must pass back unmodified.

## Configuration

| Mode | Shape | Availability |
|---|---|---|
| Adaptive | `{"type": "adaptive", "display": "summarized\|omitted"}` | All current models. On Fable/Mythos 5.x, Opus·Sonnet 5 it's **on by default** (no param needed) |
| Disabled | `{"type": "disabled"}` | Sonnet 5 (yes), Opus 5 at `effort ≤ high` (yes); Fable/Mythos 5.x — **400, can't disable**; `xhigh`/`max` + disabled — **400** |
| Extended (manual) | `{"type": "enabled", "budget_tokens": N}` | ≤4.6 models only. ≥1024, < `max_tokens`. Deprecated on 4.6, removed on 4.7+ (400) |

Not sure what your model accepts? `GET /v1/models` reports
`capabilities.thinking.types.{adaptive,enabled}` per model.

### Effort

`output_config.effort`: `low` · `medium` · `high` (default) · `xhigh` · `max`.
Effort shapes the whole response, including how much adaptive thinking happens.
`adaptive` is a thinking mode, **not** an effort value. Effort can be changed
mid-conversation per message on models that support it (beta); otherwise a
change = new cache prefix (see caching below).

### Display

| `display` | You receive | Default on |
|---|---|---|
| `summarized` | Readable summary of reasoning | Opus/Sonnet 4.6 and earlier |
| `omitted` | Empty `thinking` + full `signature` (faster time-to-text) | Fable/Mythos 5.x, Opus 5, Sonnet 5, Opus 4.8/4.7 |
| `updates` (beta) | Empty reasoning + progress-update text between tool calls | Fable 5.1 / Mythos 5.1 / Fable 5 (opt-in, header `thinking-display-updates-2026-08-18`) |

Billing is identical across `display` values (you pay for the **full** reasoning,
not the summary). `display` is invalid with `type: "disabled"`.

## Tool use with thinking

- **A tool-use loop is one assistant turn.** Don't toggle thinking inside it —
  the API silently disables thinking if you do. Toggle between turns only.
- **Pass thinking blocks back.** Required within a tool-use turn, recommended
  across turns. Blocks must be complete, unmodified, in original order; a run
  of consecutive `thinking` blocks must match what the model generated. Omit
  `redacted_thinking` blocks and the request 400s (`blocks cannot be modified`).
- **Interleaved thinking** (reasoning between tool calls) is automatic with
  adaptive on every adaptive-capable model except Haiku 4.5. Manual
  `type: "enabled"` needs the interleaved beta and changes budget accounting.
- **Progress updates** (Fable 5.x/Fable 5): short user-facing status blocks
  before tool calls; only visible under `display: "updates"`/`summarized`.

## Preservation & switching models

| Regime | Models | Behavior |
|---|---|---|
| Keep all prior thinking | Opus 4.5+, Sonnet 4.6+, Fable/Mythos 5.x, Mythos Preview | Prior blocks stay in context + cache; budget them as real history |
| Last turn only | Earlier Opus/Sonnet, all Haiku ≤4.5 | API strips older thinking blocks automatically when replayed |

- Keep the conversation **append-only**: from Fable 5.1 the API checks each
  block's `signature` against the prefix (system, tools, messages). Edited
  prefixes → 400 (default for accounts created after 2026-08-31) or dropped
  blocks, controlled by the `thinking-binding-controls-2026-08-01` beta
  (`block_binding.prefix_mismatch_behavior: "error"|"drop_block"`).
- Switching models: a model reads its own thinking blocks and older models',
  never newer ones; unreadable blocks are dropped without billing. Fable 5.1 +
  Mythos 5.1 read everything; nothing reads theirs.
- Editing instructions mid-session? Use a mid-conversation `role: "system"`
  message instead of editing the top-level `system` field — the cached prefix
  (and the thinking signatures bound to it) stays valid.
- Override retention with context editing (`clear_thinking_20251015` strategy).

## Thinking & prompt caching

- Any thinking config change (mode, `budget_tokens`, effort) invalidates cache
  breakpoints — the config is rendered into the prompt.
- During tool loops, thinking blocks are cached **with** tool results (counted
  as input tokens when read back) — this makes 1h TTL useful for agent turns.
- A block dropped by the preservation checks changes the cached prefix from
  that position on.

## Billing & context

- Thinking tokens bill as **output** tokens and count toward `max_tokens` —
  set `max_tokens` generously (or the turn truncates at `max_tokens`).
- Current-turn thinking occupies the context window for that turn; prior-turn
  thinking persists only on keep-all models (input-billed like history).
- `usage.output_tokens_details.thinking_tokens` shows the reasoning share.
- Use `POST /v1/messages/count_tokens` to budget multi-turn + thinking prompts;
  batches for thinking-heavy (>32k) requests.

## Limits that return 400

| Attempt | Result |
|---|---|
| `temperature`/`top_p`/`top_k` non-default on current models | 400 (any request; older models only while thinking is on) |
| Prefill while thinking is on | Not supported |
| `type: "enabled"` on 4.7+ / `type: "adaptive"` on ≤4.5 | 400 with a fixing hint |
| Forced tool use (`any`/`tool`) with manual thinking, or any forced use on Fable/Mythos 5.1 | 400 |
| Modified thinking block replay | 400 naming `messages.{i}.content.{j}` |

Symptom-first fixes: Anthropic's *Troubleshooting thinking* page.

## Edge cases

- **A refusal can be a reasoning-extraction guard:** on Fable 5.x, asking for
  internal reasoning in the response text may stop with
  `stop_details.category: "reasoning_extraction"`.
- **Progress-update as last block:** when a turn truncates right after a tool
  call, the final block may be a progress update — pass the turn back unchanged
  and append the `tool_result`s to continue.
- Summarized thinking is produced by a different model; it's not the raw chain
  of thought, and its length doesn't reflect billed tokens.

Next: wire it into requests → [messages.md](messages.md); stream its deltas →
[stream.md](stream.md).
