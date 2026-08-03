import type { DomainEvent } from "@nusashell/domain";

export interface SubagentRunStartedEvent extends DomainEvent {
  readonly type: "subagent.run_started";
  readonly runId: string;
  readonly conversationId: string;
  readonly providerId: string;
  readonly title?: string;
  readonly prompt: string;
  readonly parentConversationId?: string;
  readonly parentTraceId?: string;
}

export function createSubagentRunStartedEvent(
  runId: string,
  conversationId: string,
  providerId: string,
  prompt: string,
  options?: { title?: string; parentConversationId?: string; parentTraceId?: string },
  occurredAt = new Date(),
): SubagentRunStartedEvent {
  return {
    type: "subagent.run_started",
    aggregateId: runId,
    occurredAt,
    runId,
    conversationId,
    providerId,
    prompt,
    ...(options?.title ? { title: options.title } : {}),
    ...(options?.parentConversationId ? { parentConversationId: options.parentConversationId } : {}),
    ...(options?.parentTraceId ? { parentTraceId: options.parentTraceId } : {}),
  };
}
