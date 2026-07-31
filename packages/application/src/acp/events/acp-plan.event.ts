import type { DomainEvent } from "@nusashell/domain";
import type { AcpPlanStep } from "../ports/acp-client.port.js";

export interface AcpPlanEvent extends DomainEvent {
  readonly type: "acp.plan";
  readonly traceId: string;
  readonly conversationId: string;
  readonly steps: readonly AcpPlanStep[];
}

export function createAcpPlanEvent(
  traceId: string,
  conversationId: string,
  steps: readonly AcpPlanStep[],
  occurredAt = new Date(),
): AcpPlanEvent {
  return { type: "acp.plan", aggregateId: traceId, occurredAt, traceId, conversationId, steps };
}
