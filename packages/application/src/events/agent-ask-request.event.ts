import type { DomainEvent } from "@nusashell/domain";
import type { AskQuestionOption } from "../agent/services/ask-question-service.js";

export interface AgentAskRequestEvent extends DomainEvent {
  readonly type: "agent.ask_request";
  readonly traceId: string;
  readonly callId: string;
  readonly question: string;
  readonly options: readonly AskQuestionOption[];
  readonly allowFreeText: boolean;
  readonly multiSelect: boolean;
}

export function createAgentAskRequestEvent(
  traceId: string,
  callId: string,
  question: string,
  options: readonly AskQuestionOption[],
  allowFreeText: boolean,
  multiSelect: boolean,
  occurredAt = new Date(),
): AgentAskRequestEvent {
  return {
    type: "agent.ask_request",
    aggregateId: traceId,
    occurredAt,
    traceId,
    callId,
    question,
    options,
    allowFreeText,
    multiSelect,
  };
}
