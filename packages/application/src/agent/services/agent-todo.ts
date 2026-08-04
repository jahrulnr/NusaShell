export type AgentTodoStatus = "pending" | "in_progress" | "completed";

export interface AgentTodoItem {
  readonly id: string;
  readonly content: string;
  readonly status: AgentTodoStatus;
}

export interface AgentTodoList {
  readonly conversationId: string;
  readonly items: readonly AgentTodoItem[];
}

export interface AgentTodoSummary {
  readonly total: number;
  readonly pending: number;
  readonly inProgress: number;
  readonly completed: number;
}

export function summarizeTodos(items: readonly AgentTodoItem[]): AgentTodoSummary {
  let pending = 0;
  let inProgress = 0;
  let completed = 0;
  for (const item of items) {
    if (item.status === "pending") pending++;
    else if (item.status === "in_progress") inProgress++;
    else if (item.status === "completed") completed++;
  }
  return { total: items.length, pending, inProgress, completed };
}
