import type { Command } from "../../../messaging/command.js";
import type { AgentTodoItem } from "../../services/agent-todo.js";

export interface ManageTodosCommand extends Command {
  readonly kind: "manage-todos";
  readonly conversationId: string;
  readonly action: "get" | "set" | "delete";
  /** For action "set": full replacement list. For "delete": ignored. */
  readonly items?: readonly AgentTodoItem[];
  /** For action "delete": ids to remove. For "set": ignored. */
  readonly ids?: readonly string[];
}
