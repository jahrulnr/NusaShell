import type { DomainEvent } from "@nusashell/domain";

export interface SubagentRunEndedEvent extends DomainEvent {
  readonly type: "subagent.run_ended";
  readonly runId: string;
  readonly conversationId: string;
  readonly providerId: string;
  readonly ok: boolean;
  readonly summary?: string;
  readonly error?: string;
}

export function createSubagentRunEndedEvent(
  runId: string,
  conversationId: string,
  providerId: string,
  ok: boolean,
  options?: { summary?: string; error?: string },
  occurredAt = new Date(),
): SubagentRunEndedEvent {
  return {
    type: "subagent.run_ended",
    aggregateId: runId,
    occurredAt,
    runId,
    conversationId,
    providerId,
    ok,
    ...(options?.summary ? { summary: options.summary } : {}),
    ...(options?.error ? { error: options.error } : {}),
  };
}
