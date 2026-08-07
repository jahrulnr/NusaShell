/**
 * Token-efficiency telemetry records (metadata-first).
 *
 * These records intentionally store only numeric usage, timing, and status
 * metadata — never raw prompts, completions, tool output, API keys, or
 * authorization headers. `traceId` is the correlation key that joins a
 * turn summary to the provider requests it produced.
 *
 * Naming mirrors the runtime's canonical {@link AgentTokenUsage} shape
 * (`inputTokens`/`cachedInputTokens`/…) rather than OpenAI's
 * `prompt_tokens`/`completion_tokens`. Fresh (uncached) input is derived as
 * `inputTokens - cachedInputTokens`; cache hit rate as
 * `cachedInputTokens / inputTokens`.
 */

/** Canonical, provider-neutral token usage as stored in telemetry. */
export interface TelemetryTokenUsage {
  readonly inputTokens: number;
  readonly outputTokens: number;
  readonly cachedInputTokens: number;
  readonly cacheWriteTokens: number;
  readonly reasoningOutputTokens: number;
  /**
   * Whether the numbers came straight from the provider `usage` block or were
   * estimated locally. Estimated numbers must never be silently mixed with
   * provider numbers when reconciling with an upstream dashboard.
   */
  readonly source: "provider" | "estimated";
}

/** One record per provider `complete()` call (one provider round attempt). */
export interface ProviderRequestTelemetry {
  readonly kind: "provider_request";
  readonly schemaVersion: 1;
  readonly traceId: string;
  readonly conversationId?: string;
  /** ISO timestamp when the request settled (completed or failed). */
  readonly timestamp: string;
  readonly providerId?: string;
  readonly model?: string;
  /**
   * Tool/provider round index (1-based). `0` marks an out-of-loop request such
   * as the context-compaction summarizer sample.
   */
  readonly round: number;
  readonly usage?: TelemetryTokenUsage;
  readonly timing: {
    readonly startedAt: string;
    readonly completedAt: string;
    readonly latencyMs: number;
  };
  readonly outcome: {
    readonly status: "completed" | "failed";
    /** Provider finish reason (`stop`, `end_turn`, `length`, …) when known. */
    readonly finishReason?: string;
    readonly errorCode?: string;
  };
}

/** One aggregate record emitted when a user turn settles. */
export interface AgentTurnTelemetry {
  readonly kind: "agent_turn";
  readonly schemaVersion: 1;
  readonly traceId: string;
  readonly conversationId?: string;
  readonly startedAt: string;
  readonly completedAt: string;
  readonly durationMs: number;
  readonly providerId?: string;
  readonly model?: string;
  readonly status: "completed" | "failed" | "cancelled" | "superseded";
  /** Provider rounds consumed by the turn (proxy for provider requests). */
  readonly rounds: number;
  readonly tools: {
    readonly calls: number;
    readonly succeeded: number;
    readonly failed: number;
  };
  readonly compaction: {
    /** Number of memento compactions attached to the settled turn (0 or 1). */
    readonly count: number;
  };
  readonly usage?: TelemetryTokenUsage;
}

export type TelemetryRecord = ProviderRequestTelemetry | AgentTurnTelemetry;
