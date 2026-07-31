import type { DomainEvent } from "@nusashell/domain";

export interface AcpTextDeltaEvent extends DomainEvent {
  readonly type: "acp.text_delta";
  readonly traceId: string;
  readonly conversationId: string;
  readonly delta: string;
}

export function createAcpTextDeltaEvent(
  traceId: string,
  conversationId: string,
  delta: string,
  occurredAt = new Date(),
): AcpTextDeltaEvent {
  return {
    type: "acp.text_delta",
    aggregateId: traceId,
    occurredAt,
    traceId,
    conversationId,
    delta,
  };
}
