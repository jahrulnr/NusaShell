import type { DomainEvent } from "@nusashell/domain";
import type { AsyncToolStatus } from "../agent/services/async-tool-runtime.js";

export interface AgentToolJobUpdateEvent extends DomainEvent {
  readonly type: "agent.tool_job_update";
  readonly handleId: string;
  readonly conversationId: string;
  readonly status: AsyncToolStatus;
  readonly tail: string;
  readonly bytes: number;
  readonly streamSeq: number;
}

export function createAgentToolJobUpdateEvent(
  handleId: string,
  conversationId: string,
  status: AsyncToolStatus,
  tail: string,
  bytes: number,
  streamSeq: number,
  occurredAt = new Date(),
): AgentToolJobUpdateEvent {
  return {
    type: "agent.tool_job_update",
    aggregateId: conversationId,
    occurredAt,
    handleId,
    conversationId,
    status,
    tail,
    bytes,
    streamSeq,
  };
}
