import type { DomainEvent } from "@nusashell/domain";

export interface AgentLearningUpdatedEvent extends DomainEvent {
  readonly type: "agent.learning_updated";
  readonly kinds: readonly string[];
  readonly summary: string;
  readonly reviewTraceId: string;
}

export function createLearningUpdatedEvent(
  reviewTraceId: string,
  kinds: readonly string[],
  summary: string,
  occurredAt = new Date(),
): AgentLearningUpdatedEvent {
  return {
    type: "agent.learning_updated",
    aggregateId: reviewTraceId,
    occurredAt,
    kinds,
    summary,
    reviewTraceId,
  };
}
