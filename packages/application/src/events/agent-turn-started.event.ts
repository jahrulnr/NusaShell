import type { DomainEvent } from "@nusashell/domain";

export interface AgentTurnStartedEvent extends DomainEvent {
  readonly type: "agent.turn_started";
  readonly conversationId?: string;
}

export function createAgentTurnStartedEvent(
  traceId: string,
  conversationId?: string,
  occurredAt = new Date(),
): AgentTurnStartedEvent {
  return {
    type: "agent.turn_started",
    aggregateId: traceId,
    occurredAt,
    ...(conversationId !== undefined ? { conversationId } : {}),
  };
}
