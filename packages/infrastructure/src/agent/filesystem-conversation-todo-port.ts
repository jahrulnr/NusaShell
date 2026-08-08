import { mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import type { ConversationTodoPort, AgentTodoItem } from "@nusashell/application";

const TODO_FILE = "conversation-todos.json";

/** Durable conversation-scoped TODO state for the desktop runtime. */
export class FilesystemConversationTodoPort implements ConversationTodoPort {
  private readonly store = new Map<string, readonly AgentTodoItem[]>();
  private readonly path: string;

  constructor(root: string) {
    this.path = join(root, TODO_FILE);
    this.load();
  }

  get(conversationId: string): readonly AgentTodoItem[] {
    return this.store.get(conversationId) ?? [];
  }

  set(conversationId: string, items: readonly AgentTodoItem[]): void {
    this.store.set(conversationId, [...items]);
    this.persist();
  }

  clear(conversationId: string): void {
    if (!this.store.delete(conversationId)) return;
    this.persist();
  }

  private load(): void {
    try {
      const parsed = JSON.parse(readFileSync(this.path, "utf8")) as Record<string, unknown>;
      for (const [conversationId, value] of Object.entries(parsed)) {
        if (!Array.isArray(value)) continue;
        const items = value.filter(isTodoItem);
        this.store.set(conversationId, items);
      }
    } catch {
      // Missing or corrupt optional state must not prevent the shell from booting.
    }
  }

  private persist(): void {
    mkdirSync(dirname(this.path), { recursive: true });
    const temporaryPath = `${this.path}.tmp`;
    const value = Object.fromEntries(this.store.entries());
    writeFileSync(temporaryPath, `${JSON.stringify(value, null, 2)}\n`, { mode: 0o600 });
    renameSync(temporaryPath, this.path);
  }
}

function isTodoItem(value: unknown): value is AgentTodoItem {
  if (!value || typeof value !== "object") return false;
  const item = value as Partial<AgentTodoItem>;
  return typeof item.id === "string"
    && typeof item.content === "string"
    && (item.status === "pending" || item.status === "in_progress" || item.status === "completed");
}
