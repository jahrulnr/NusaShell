import type { DomainEvent } from "@nusashell/domain";
import type { AcpToolStatus } from "../ports/acp-client.port.js";

export interface AcpToolCallUpdateEvent extends DomainEvent {
  readonly type: "acp.tool_call_update";
  readonly traceId: string;
  readonly conversationId: string;
  readonly callId: string;
  readonly status: AcpToolStatus;
  readonly summary?: string | undefined;
}

export function createAcpToolCallUpdateEvent(
  traceId: string,
  conversationId: string,
  callId: string,
  status: AcpToolStatus,
  summary: string | undefined,
  occurredAt = new Date(),
): AcpToolCallUpdateEvent {
  return { type: "acp.tool_call_update", aggregateId: traceId, occurredAt, traceId, conversationId, callId, status, summary };
}
