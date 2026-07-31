import type { DomainEvent } from "@nusashell/domain";

export interface AgentCancelRequestedEvent extends DomainEvent {
  readonly type: "agent.cancel_requested";
}

export function createAgentCancelRequestedEvent(
  traceId: string,
  occurredAt = new Date(),
): AgentCancelRequestedEvent {
  return {
    type: "agent.cancel_requested",
    aggregateId: traceId,
    occurredAt,
  };
}
