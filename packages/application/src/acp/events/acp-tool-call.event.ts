import type { DomainEvent } from "@nusashell/domain";
import type { AcpToolCall } from "../ports/acp-client.port.js";

export interface AcpToolCallEvent extends DomainEvent {
  readonly type: "acp.tool_call";
  readonly traceId: string;
  readonly conversationId: string;
  readonly call: AcpToolCall;
}

export function createAcpToolCallEvent(
  traceId: string,
  conversationId: string,
  call: AcpToolCall,
  occurredAt = new Date(),
): AcpToolCallEvent {
  return { type: "acp.tool_call", aggregateId: traceId, occurredAt, traceId, conversationId, call };
}
