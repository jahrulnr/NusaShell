# Token-efficiency telemetry

Thin, metadata-first telemetry that measures how efficient one **user turn** is
end-to-end — not just how cheap a single provider request looks. It implements
Phase 1 (“Measurement foundation”) of the token-efficiency proposal: correlate
provider requests and turn summaries by `traceId`, persist locally as JSONL, and
report `cost per successful turn`, `fresh tokens per turn`, `provider requests
per turn`, and `failure waste` — without storing any prompt content.

## What is recorded

`traceId` is the correlation key. A single turn produces one aggregate record
and one provider-request record per provider `complete()` call.

- **Provider request** (`provider_request`): per `complete()` call — `traceId`,
  `round`, `providerId`, `model`, `usage`, `timing` (latency), and `outcome`
  (`completed`/`failed`, finish reason, error code). Router failover produces one
  record per candidate tried; the context-compaction summarizer sample is
  recorded with `round: 0`.
- **Agent turn** (`agent_turn`): emitted when the turn settles — `status`
  (`completed`/`failed`/`cancelled`/`superseded`), `rounds`, tool call counts,
  compaction count, cumulative `usage`, and `durationMs`.

Token usage mirrors the runtime's canonical `AgentTokenUsage`
(`inputTokens`/`cachedInputTokens`/`outputTokens`/`reasoningOutputTokens`).
Derived metrics: fresh input = `inputTokens - cachedInputTokens`; cache hit rate
= `cachedInputTokens / inputTokens`.

## Design principles

- **Metadata-first, content-optional.** Only numeric usage, timing, and status
  are stored. No raw prompts, completions, tool output, API keys, or
  authorization headers — consistent with
  [`security-boundary.md`](./security-boundary.md).
- **Provider usage is the source of truth.** Numbers come from the provider
  `usage` block; every usage record is tagged `source: "provider"` (estimates,
  if ever added, must be labeled `estimated` and never silently mixed).
- **Fire-and-forget.** The sink never throws and never blocks a turn: writes are
  serialized on an async queue and all failures are swallowed (optionally
  observed for logging). A telemetry failure can never fail a user turn.
- **Turn-level over request-level.** One cheap request does not imply a cheap
  turn; a turn can fan out into many provider rounds, tool loops, retries, and
  compactions.

## Integration points (Clean Architecture)

The `domain` layer is untouched. Telemetry lives in `application` (port + logic)
and `infrastructure` (JSONL adapter), wired in the composition root.

| Concern | Where |
| --- | --- |
| `TelemetryPort` + record types + helpers | `packages/application/src/telemetry/` |
| Per-request capture | `TelemetryAgentProvider` decorator wraps each `AgentProvider` (`withTelemetry`) so every `complete()` is recorded, including router failover and the compaction summarizer |
| Per-turn capture | `RunAgentTurnHandler` records one aggregate via `buildTurnTelemetry` on the success and failure paths (injected through its `hooks.telemetry`) |
| JSONL persistence | `JsonlTelemetryWriter` (`packages/infrastructure/src/telemetry/`) |
| Wiring | `apps/backend/src/composers/agent-runtime.ts` wraps providers; `container.ts` builds the sink; `bus-registration.ts` passes it into the handler hooks |

## Storage

Append-only, newline-delimited JSON under `{userData}/telemetry/`, rotated per
UTC day:

```text
{userData}/telemetry/provider-requests-YYYY-MM-DD.jsonl
{userData}/telemetry/agent-turns-YYYY-MM-DD.jsonl
```

Files older than the retention window (default 30 days) are pruned lazily on the
first write. A future phase may add a SQLite projection for in-app analytics; the
JSONL remains the durable spine.

## Configuration

| Env var | Default | Purpose |
| --- | ---: | --- |
| `NUSASHELL_TELEMETRY` | `true` | Enable/disable telemetry recording |
| `NUSASHELL_TELEMETRY_RETENTION_DAYS` | `30` | Daily-file retention window |

Telemetry only writes when a directory is configured (`telemetryDir`). The
desktop shell resolves it to `{userData}/telemetry`. Non-desktop/test callers
that omit the directory get a no-op sink (via `withTelemetry(provider,
undefined)`), keeping the no-telemetry path allocation-free.

## Reporting

`scripts/telemetry-report.mjs` reads a telemetry directory and computes the
turn-level metrics (cache hit rate, fresh-token ratio, provider requests per
turn, rounds median/p95, fresh tokens per completed turn, failure waste ratio),
or exports the turns as CSV:

```bash
pnpm telemetry:report <telemetryDir>              # JSON summary
pnpm telemetry:report <telemetryDir> --format csv # per-turn CSV
```

`costPerCompletedTurn` is reported as `null`: the current provider adapters parse
token `usage` but not upstream dollar cost. Adding cost passthrough (e.g.
OpenRouter `usage.cost`) is a follow-up; when present it would populate the
telemetry `cost` field and this metric.

## Scope

This is Phase 1 (measurement). Prompt-composition segment breakdowns, an in-app
**AI Usage** analytics view, and adaptive optimization (cache-aware assembly,
dynamic context budgets, model escalation) are later phases and are intentionally
out of scope here — optimization should follow evidence, not precede it.
