import type { DomainEvent } from "@nusashell/domain";

export interface AgentTurnSupersededEvent extends DomainEvent {
  readonly type: "agent.turn_superseded";
  readonly byTraceId: string;
}

export function createAgentTurnSupersededEvent(
  traceId: string,
  byTraceId: string,
  occurredAt = new Date(),
): AgentTurnSupersededEvent {
  return {
    type: "agent.turn_superseded",
    aggregateId: traceId,
    occurredAt,
    byTraceId,
  };
}
