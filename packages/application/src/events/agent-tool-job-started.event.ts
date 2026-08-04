import type { DomainEvent } from "@nusashell/domain";
import type { AsyncToolKind } from "../agent/services/async-tool-runtime.js";

export interface AgentToolJobStartedEvent extends DomainEvent {
  readonly type: "agent.tool_job_started";
  readonly handleId: string;
  readonly conversationId: string;
  readonly kind: AsyncToolKind;
  readonly toolName: string;
  readonly pluginId?: string;
  readonly argsSummary: string;
  readonly traceId?: string;
}

export function createAgentToolJobStartedEvent(
  handleId: string,
  conversationId: string,
  kind: AsyncToolKind,
  toolName: string,
  argsSummary: string,
  options?: { readonly pluginId?: string; readonly traceId?: string; readonly occurredAt?: Date },
): AgentToolJobStartedEvent {
  const occurredAt = options?.occurredAt ?? new Date();
  return {
    type: "agent.tool_job_started",
    aggregateId: conversationId,
    occurredAt,
    handleId,
    conversationId,
    kind,
    toolName,
    argsSummary,
    ...(options?.pluginId ? { pluginId: options.pluginId } : {}),
    ...(options?.traceId ? { traceId: options.traceId } : {}),
  };
}
