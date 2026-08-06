import type { AgentTokenUsage } from "../agent/ports/agent-provider.port.js";
import type { TelemetryTokenUsage } from "./telemetry.types.js";

/** Project the runtime's canonical usage into a stored telemetry usage. */
export function toTelemetryUsage(
  usage: AgentTokenUsage,
  source: TelemetryTokenUsage["source"] = "provider",
): TelemetryTokenUsage {
  return {
    inputTokens: usage.inputTokens,
    outputTokens: usage.outputTokens,
    cachedInputTokens: usage.cachedInputTokens,
    cacheWriteTokens: usage.cacheWriteTokens,
    reasoningOutputTokens: usage.reasoningOutputTokens,
    source,
  };
}

/** Fresh (uncached) input tokens: `inputTokens - cachedInputTokens`, floored at 0. */
export function freshInputTokens(usage: TelemetryTokenUsage): number {
  return Math.max(0, usage.inputTokens - usage.cachedInputTokens);
}

/** Cache hit rate for a usage record: `cachedInputTokens / inputTokens`. */
export function cacheHitRate(usage: TelemetryTokenUsage): number {
  return usage.inputTokens > 0 ? usage.cachedInputTokens / usage.inputTokens : 0;
}
