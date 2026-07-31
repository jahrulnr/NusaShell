import type { DomainEvent } from "@nusashell/domain";
import type { AcpPermissionOption } from "../ports/acp-client.port.js";

export interface AcpPermissionRequestEvent extends DomainEvent {
  readonly type: "acp.permission_request";
  readonly traceId: string;
  readonly conversationId: string;
  readonly requestId: string;
  readonly toolTitle: string;
  readonly detail?: string | undefined;
  readonly options: readonly AcpPermissionOption[];
}

export function createAcpPermissionRequestEvent(
  traceId: string,
  conversationId: string,
  requestId: string,
  toolTitle: string,
  detail: string | undefined,
  options: readonly AcpPermissionOption[],
  occurredAt = new Date(),
): AcpPermissionRequestEvent {
  return {
    type: "acp.permission_request",
    aggregateId: traceId,
    occurredAt,
    traceId,
    conversationId,
    requestId,
    toolTitle,
    detail,
    options,
  };
}
