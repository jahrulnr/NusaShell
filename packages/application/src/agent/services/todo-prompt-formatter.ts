import type { AgentTodoItem } from "./agent-todo.js";

const STATUS_GLYPH: Readonly<Record<AgentTodoItem["status"], string>> = {
  pending: "[ ]",
  in_progress: "[~]",
  completed: "[x]",
};

/**
 * Format a todo list into a system-prompt block.
 * Only items with status !== "completed" are injected (completed items are
 * noise for the model — the strip still shows them for the user).
 * Returns undefined when there are no incomplete items (no block injected),
 * mirroring formatMemoryPrompt's empty → undefined contract.
 */
export function formatTodoPrompt(items: readonly AgentTodoItem[]): string | undefined {
  const incomplete = items.filter((item) => item.status !== "completed");
  if (incomplete.length === 0) return undefined;
  const lines = incomplete.map(
    (item) => `${STATUS_GLYPH[item.status]} ${item.content}`,
  );
  return `CURRENT TASKS (agent-owned checklist — user may delete items)\n${lines.join("\n")}`;
}
