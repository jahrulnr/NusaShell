import type { DomainEvent } from "@nusashell/domain";
import type { AgentToolCall } from "../agent/ports/agent-provider.port.js";

export interface AgentToolCallStartEvent extends DomainEvent {
  readonly type: "agent.tool_call_start";
  readonly call: AgentToolCall;
}

export function createAgentToolCallStartEvent(
  traceId: string,
  call: AgentToolCall,
  occurredAt = new Date(),
): AgentToolCallStartEvent {
  return {
    type: "agent.tool_call_start",
    aggregateId: traceId,
    occurredAt,
    call,
  };
}
