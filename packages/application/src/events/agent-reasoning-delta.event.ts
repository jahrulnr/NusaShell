import type { DomainEvent } from "@nusashell/domain";

export interface AgentReasoningDeltaEvent extends DomainEvent {
  readonly type: "agent.reasoning_delta";
  readonly delta: string;
}

export function createAgentReasoningDeltaEvent(
  traceId: string,
  delta: string,
  occurredAt = new Date(),
): AgentReasoningDeltaEvent {
  return {
    type: "agent.reasoning_delta",
    aggregateId: traceId,
    occurredAt,
    delta,
  };
}
