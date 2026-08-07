import type { AgentTokenUsage } from "../agent/ports/agent-provider.port.js";
import { toTelemetryUsage } from "./telemetry-usage.js";
import type { AgentTurnTelemetry } from "./telemetry.types.js";

export interface BuildTurnTelemetryInput {
  readonly traceId: string;
  readonly conversationId?: string;
  readonly status: AgentTurnTelemetry["status"];
  readonly startedAtMs: number;
  readonly completedAtMs: number;
  readonly rounds: number;
  /** Executions accumulated during the turn (from result/partial). */
  readonly toolCalls: readonly { readonly ok: boolean }[];
  /** Whether a compaction checkpoint was attached to the settled turn. */
  readonly hasCompaction: boolean;
  readonly model?: string;
  readonly providerId?: string;
  readonly usage?: AgentTokenUsage;
}

/**
 * Pure builder for the per-turn aggregate telemetry record. Kept out of the
 * command handler so it stays trivially unit-testable and side-effect free.
 */
export function buildTurnTelemetry(input: BuildTurnTelemetryInput): AgentTurnTelemetry {
  const succeeded = input.toolCalls.filter((call) => call.ok).length;
  return {
    kind: "agent_turn",
    schemaVersion: 1,
    traceId: input.traceId,
    ...(input.conversationId ? { conversationId: input.conversationId } : {}),
    startedAt: new Date(input.startedAtMs).toISOString(),
    completedAt: new Date(input.completedAtMs).toISOString(),
    durationMs: Math.max(0, input.completedAtMs - input.startedAtMs),
    ...(input.providerId ? { providerId: input.providerId } : {}),
    ...(input.model ? { model: input.model } : {}),
    status: input.status,
    rounds: input.rounds,
    tools: {
      calls: input.toolCalls.length,
      succeeded,
      failed: input.toolCalls.length - succeeded,
    },
    compaction: { count: input.hasCompaction ? 1 : 0 },
    ...(input.usage ? { usage: toTelemetryUsage(input.usage) } : {}),
  };
}
