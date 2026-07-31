import type { DomainEvent } from "@nusashell/domain";

export interface AcpThoughtDeltaEvent extends DomainEvent {
  readonly type: "acp.thought_delta";
  readonly traceId: string;
  readonly conversationId: string;
  readonly delta: string;
}

export function createAcpThoughtDeltaEvent(
  traceId: string,
  conversationId: string,
  delta: string,
  occurredAt = new Date(),
): AcpThoughtDeltaEvent {
  return { type: "acp.thought_delta", aggregateId: traceId, occurredAt, traceId, conversationId, delta };
}
