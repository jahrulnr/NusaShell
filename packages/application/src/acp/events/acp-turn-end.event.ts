import type { DomainEvent } from "@nusashell/domain";

export interface AcpTurnEndEvent extends DomainEvent {
  readonly type: "acp.turn_end";
  readonly traceId: string;
  readonly conversationId: string;
  readonly ok: boolean;
  readonly error?: string | undefined;
}

export function createAcpTurnEndEvent(
  traceId: string,
  conversationId: string,
  ok: boolean,
  error: string | undefined,
  occurredAt = new Date(),
): AcpTurnEndEvent {
  return { type: "acp.turn_end", aggregateId: traceId, occurredAt, traceId, conversationId, ok, error };
}
