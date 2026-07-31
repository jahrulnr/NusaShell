import type { DomainEvent } from "@nusashell/domain";

export type AgentTurnEndReason = "completed" | "cancelled" | "failed" | "superseded";

export interface AgentTurnEndEvent extends DomainEvent {
  readonly type: "agent.turn_end";
  readonly reason: AgentTurnEndReason;
}

export function createAgentTurnEndEvent(
  traceId: string,
  reason: AgentTurnEndReason,
  occurredAt = new Date(),
): AgentTurnEndEvent {
  return {
    type: "agent.turn_end",
    aggregateId: traceId,
    occurredAt,
    reason,
  };
}
