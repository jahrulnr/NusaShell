import { ApplicationError } from "../../errors/application-error.js";
import type { ConversationTodoPort } from "../ports/conversation-todo.port.js";
import type { AgentTodoItem, AgentTodoStatus } from "./agent-todo.js";
import { summarizeTodos } from "./agent-todo.js";

const VALID_STATUSES: ReadonlySet<AgentTodoStatus> = new Set(["pending", "in_progress", "completed"]);
const MAX_ITEMS = 50;
const MAX_CONTENT_CHARS = 500;

/**
 * Replace the entire conversation todo list (Claude TodoWrite style).
 * Empty `items` clears the list. Requires a conversationId on the turn context.
 * Returns summary counts + the current list.
 */
export async function execTodo(
  todoPort: ConversationTodoPort | undefined,
  args: Readonly<Record<string, unknown>>,
  turnId: string,
  conversationId: string | undefined,
): Promise<unknown> {
  if (!todoPort) {
    return { ok: false, error: { code: "todo_not_configured", message: "Todo tracking is not available." } };
  }
  if (!conversationId) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "todo tool requires a conversation context");
  }
  const rawItems = args.items;
  if (!Array.isArray(rawItems)) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "items must be an array");
  }
  if (rawItems.length > MAX_ITEMS) {
    throw new ApplicationError("AGENT_INVALID_INPUT", `items must have at most ${MAX_ITEMS} entries`);
  }
  const items: AgentTodoItem[] = [];
  const seenIds = new Set<string>();
  for (const entry of rawItems) {
    if (typeof entry !== "object" || entry === null) {
      throw new ApplicationError("AGENT_INVALID_INPUT", "each item must be an object");
    }
    const record = entry as Record<string, unknown>;
    const id = typeof record.id === "string" ? record.id.trim() : "";
    const content = typeof record.content === "string" ? record.content.trim() : "";
    const status = typeof record.status === "string" ? record.status : "";
    if (!id) throw new ApplicationError("AGENT_INVALID_INPUT", "each item requires a non-empty id");
    if (seenIds.has(id)) throw new ApplicationError("AGENT_INVALID_INPUT", `duplicate item id: ${id}`);
    seenIds.add(id);
    if (!content) throw new ApplicationError("AGENT_INVALID_INPUT", "each item requires non-empty content");
    if (content.length > MAX_CONTENT_CHARS) {
      throw new ApplicationError("AGENT_INVALID_INPUT", `item content exceeds ${MAX_CONTENT_CHARS} chars`);
    }
    if (!VALID_STATUSES.has(status as AgentTodoStatus)) {
      throw new ApplicationError("AGENT_INVALID_INPUT", `item status must be pending, in_progress, or completed`);
    }
    items.push({ id, content, status: status as AgentTodoStatus });
  }
  todoPort.set(conversationId, items);
  const current = todoPort.get(conversationId);
  return {
    ok: true,
    conversationId,
    ...summarizeTodos(current),
    items: current,
  };
}

/**
 * Delete specific todo items by id (user-initiated from the strip UI).
 * Removes the ids from the runtime port so they do not reappear in the next
 * prompt injection.
 */
export async function execTodoDelete(
  todoPort: ConversationTodoPort | undefined,
  args: Readonly<Record<string, unknown>>,
  conversationId: string | undefined,
): Promise<unknown> {
  if (!todoPort) {
    return { ok: false, error: { code: "todo_not_configured", message: "Todo tracking is not available." } };
  }
  if (!conversationId) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "todos_delete requires a conversation context");
  }
  const rawIds = args.ids;
  if (!Array.isArray(rawIds)) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "ids must be an array");
  }
  const idsToDelete = new Set<string>();
  for (const entry of rawIds) {
    if (typeof entry === "string" && entry.trim()) idsToDelete.add(entry.trim());
  }
  const current = todoPort.get(conversationId);
  const remaining = current.filter((item) => !idsToDelete.has(item.id));
  todoPort.set(conversationId, remaining);
  return {
    ok: true,
    conversationId,
    ...summarizeTodos(remaining),
    items: remaining,
  };
}
