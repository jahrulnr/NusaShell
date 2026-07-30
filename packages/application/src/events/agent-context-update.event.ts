import type { DomainEvent } from "@nusashell/domain";
import type { AgentTokenUsage } from "../agent/ports/agent-provider.port.js";

export interface AgentContextUpdateEvent extends DomainEvent {
  readonly type: "agent.context";
  readonly estimatedTokens: number;
  readonly usage?: AgentTokenUsage;
}

export function createAgentContextUpdateEvent(
  traceId: string,
  estimatedTokens: number,
  usage?: AgentTokenUsage,
  occurredAt = new Date(),
): AgentContextUpdateEvent {
  return {
    type: "agent.context",
    aggregateId: traceId,
    occurredAt,
    estimatedTokens,
    ...(usage ? { usage } : {}),
  };
}
