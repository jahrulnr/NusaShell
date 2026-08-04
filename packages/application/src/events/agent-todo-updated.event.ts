import type { DomainEvent } from "@nusashell/domain";
import type { AgentTodoItem } from "../agent/services/agent-todo.js";

export interface AgentTodoUpdatedEvent extends DomainEvent {
  readonly type: "agent.todo_updated";
  readonly conversationId: string;
  readonly items: readonly AgentTodoItem[];
}

export function createAgentTodoUpdatedEvent(
  conversationId: string,
  items: readonly AgentTodoItem[],
  occurredAt = new Date(),
): AgentTodoUpdatedEvent {
  return {
    type: "agent.todo_updated",
    aggregateId: conversationId,
    occurredAt,
    conversationId,
    items: [...items],
  };
}
