import type { DomainEvent } from "@nusashell/domain";
import type { AgentToolExecution } from "../agent/services/agent-turn-runner.js";

export interface AgentToolCallEndEvent extends DomainEvent {
  readonly type: "agent.tool_call_end";
  readonly execution: AgentToolExecution;
}

export function createAgentToolCallEndEvent(
  traceId: string,
  execution: AgentToolExecution,
  occurredAt = new Date(),
): AgentToolCallEndEvent {
  return {
    type: "agent.tool_call_end",
    aggregateId: traceId,
    occurredAt,
    execution,
  };
}
