import type { DomainEvent } from "@nusashell/domain";
import type { AcpSessionState } from "../ports/acp-client.port.js";

export interface AcpSessionStateEvent extends DomainEvent {
  readonly type: "acp.session_state";
  readonly traceId: string;
  readonly conversationId: string;
  readonly state: AcpSessionState;
}

export function createAcpSessionStateEvent(
  traceId: string,
  conversationId: string,
  state: AcpSessionState,
  occurredAt = new Date(),
): AcpSessionStateEvent {
  return { type: "acp.session_state", aggregateId: traceId, occurredAt, traceId, conversationId, state };
}
