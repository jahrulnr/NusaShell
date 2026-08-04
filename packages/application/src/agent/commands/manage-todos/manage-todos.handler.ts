import { ApplicationError } from "../../../errors/application-error.js";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { ConversationTodoPort } from "../../ports/conversation-todo.port.js";
import type { AgentTodoItem, AgentTodoStatus } from "../../services/agent-todo.js";
import { summarizeTodos } from "../../services/agent-todo.js";
import type { ManageTodosCommand } from "./manage-todos.command.js";

const VALID_STATUSES: ReadonlySet<AgentTodoStatus> = new Set(["pending", "in_progress", "completed"]);
const MAX_ITEMS = 50;
const MAX_CONTENT_CHARS = 500;

export interface ManageTodosResult {
  readonly ok: true;
  readonly conversationId: string;
  readonly total: number;
  readonly pending: number;
  readonly inProgress: number;
  readonly completed: number;
  readonly items: readonly AgentTodoItem[];
}

export class ManageTodosHandler implements CommandHandler<ManageTodosCommand, ManageTodosResult> {
  constructor(
    private readonly todoPort: ConversationTodoPort,
    private readonly onUpdated?: (conversationId: string, items: readonly AgentTodoItem[]) => void,
  ) {}

  async handle(command: ManageTodosCommand): Promise<ManageTodosResult> {
    if (!command.conversationId) {
      throw new ApplicationError("AGENT_INVALID_INPUT", "conversationId is required");
    }
    let items: readonly AgentTodoItem[];
    if (command.action === "get") {
      items = this.todoPort.get(command.conversationId);
    } else if (command.action === "set") {
      items = this.validateAndSet(command.conversationId, command.items ?? []);
    } else if (command.action === "delete") {
      const idsToDelete = new Set((command.ids ?? []).filter((id): id is string => typeof id === "string" && id.trim().length > 0).map((id) => id.trim()));
      const current = this.todoPort.get(command.conversationId);
      items = current.filter((item) => !idsToDelete.has(item.id));
      this.todoPort.set(command.conversationId, items);
    } else {
      throw new ApplicationError("AGENT_INVALID_INPUT", `Unsupported todos action: ${command.action}`);
    }
    if (command.action !== "get") this.onUpdated?.(command.conversationId, items);
    return { ok: true, conversationId: command.conversationId, ...summarizeTodos(items), items };
  }

  private validateAndSet(conversationId: string, raw: readonly AgentTodoItem[]): readonly AgentTodoItem[] {
    if (raw.length > MAX_ITEMS) {
      throw new ApplicationError("AGENT_INVALID_INPUT", `items must have at most ${MAX_ITEMS} entries`);
    }
    const items: AgentTodoItem[] = [];
    const seenIds = new Set<string>();
    for (const entry of raw) {
      const id = typeof entry.id === "string" ? entry.id.trim() : "";
      const content = typeof entry.content === "string" ? entry.content.trim() : "";
      const status = entry.status;
      if (!id) throw new ApplicationError("AGENT_INVALID_INPUT", "each item requires a non-empty id");
      if (seenIds.has(id)) throw new ApplicationError("AGENT_INVALID_INPUT", `duplicate item id: ${id}`);
      seenIds.add(id);
      if (!content) throw new ApplicationError("AGENT_INVALID_INPUT", "each item requires non-empty content");
      if (content.length > MAX_CONTENT_CHARS) {
        throw new ApplicationError("AGENT_INVALID_INPUT", `item content exceeds ${MAX_CONTENT_CHARS} chars`);
      }
      if (!VALID_STATUSES.has(status)) {
        throw new ApplicationError("AGENT_INVALID_INPUT", "item status must be pending, in_progress, or completed");
      }
      items.push({ id, content, status });
    }
    this.todoPort.set(conversationId, items);
    return this.todoPort.get(conversationId);
  }
}
