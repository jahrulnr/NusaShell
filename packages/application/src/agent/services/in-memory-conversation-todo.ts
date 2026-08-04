import type { AgentTodoItem } from "../services/agent-todo.js";
import type { ConversationTodoPort } from "../ports/conversation-todo.port.js";

export class InMemoryConversationTodoPort implements ConversationTodoPort {
  private readonly store = new Map<string, readonly AgentTodoItem[]>();

  get(conversationId: string): readonly AgentTodoItem[] {
    return this.store.get(conversationId) ?? [];
  }

  set(conversationId: string, items: readonly AgentTodoItem[]): void {
    this.store.set(conversationId, [...items]);
  }

  clear(conversationId: string): void {
    this.store.delete(conversationId);
  }
}
