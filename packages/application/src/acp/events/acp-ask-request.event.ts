import type { DomainEvent } from "@nusashell/domain";
import type { AcpAskOption } from "../ports/acp-client.port.js";

export interface AcpAskRequestEvent extends DomainEvent {
  readonly type: "acp.ask_request";
  readonly traceId: string;
  readonly conversationId: string;
  readonly requestId: string;
  readonly question: string;
  readonly options?: readonly AcpAskOption[] | undefined;
  readonly multiSelect?: boolean | undefined;
  readonly allowFreeText?: boolean | undefined;
}

export function createAcpAskRequestEvent(
  traceId: string,
  conversationId: string,
  requestId: string,
  question: string,
  options: readonly AcpAskOption[] | undefined,
  multiSelect: boolean | undefined,
  allowFreeText: boolean | undefined,
  occurredAt = new Date(),
): AcpAskRequestEvent {
  return {
    type: "acp.ask_request",
    aggregateId: traceId,
    occurredAt,
    traceId,
    conversationId,
    requestId,
    question,
    options,
    multiSelect,
    allowFreeText,
  };
}
