import type { DomainEvent } from "@nusashell/domain";

export interface AgentTextDeltaEvent extends DomainEvent {
  readonly type: "agent.text_delta";
  readonly delta: string;
}

export function createAgentTextDeltaEvent(
  traceId: string,
  delta: string,
  occurredAt = new Date(),
): AgentTextDeltaEvent {
  return {
    type: "agent.text_delta",
    aggregateId: traceId,
    occurredAt,
    delta,
  };
}
