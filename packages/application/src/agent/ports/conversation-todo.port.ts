import type { AgentTodoItem } from "../services/agent-todo.js";

export interface ConversationTodoPort {
  get(conversationId: string): readonly AgentTodoItem[];
  set(conversationId: string, items: readonly AgentTodoItem[]): void;
  clear(conversationId: string): void;
}
