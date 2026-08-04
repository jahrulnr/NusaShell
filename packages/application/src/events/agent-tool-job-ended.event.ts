import type { DomainEvent } from "@nusashell/domain";
import type { AsyncToolEndReason } from "../agent/services/async-tool-runtime.js";

export interface AgentToolJobEndedEvent extends DomainEvent {
  readonly type: "agent.tool_job_ended";
  readonly handleId: string;
  readonly conversationId: string;
  readonly ok: boolean;
  readonly reason: AsyncToolEndReason;
  readonly error?: string;
  readonly output?: unknown;
}

export function createAgentToolJobEndedEvent(
  handleId: string,
  conversationId: string,
  ok: boolean,
  reason: AsyncToolEndReason,
  options?: { readonly error?: string; readonly output?: unknown; readonly occurredAt?: Date },
): AgentToolJobEndedEvent {
  const occurredAt = options?.occurredAt ?? new Date();
  return {
    type: "agent.tool_job_ended",
    aggregateId: conversationId,
    occurredAt,
    handleId,
    conversationId,
    ok,
    reason,
    ...(options?.error !== undefined ? { error: options.error } : {}),
    ...(options?.output !== undefined ? { output: options.output } : {}),
  };
}
