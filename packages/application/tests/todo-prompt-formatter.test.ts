import { describe, expect, it } from "vitest";
import { formatTodoPrompt } from "../src/index.js";
import type { AgentTodoItem } from "../src/index.js";

function item(id: string, content: string, status: AgentTodoItem["status"] = "pending"): AgentTodoItem {
  return { id, content, status };
}

describe("formatTodoPrompt", () => {
  it("returns undefined when items is empty", () => {
    expect(formatTodoPrompt([])).toBeUndefined();
  });

  it("returns undefined when all items are completed", () => {
    expect(formatTodoPrompt([
      item("1", "done", "completed"),
      item("2", "also done", "completed"),
    ])).toBeUndefined();
  });

  it("includes only incomplete items", () => {
    const result = formatTodoPrompt([
      item("1", "pending task"),
      item("2", "done task", "completed"),
      item("3", "active task", "in_progress"),
    ]);
    expect(result).toBeDefined();
    expect(result).toContain("pending task");
    expect(result).toContain("active task");
    expect(result).not.toContain("done task");
  });

  it("uses status glyphs", () => {
    const result = formatTodoPrompt([
      item("1", "pending task"),
      item("2", "active task", "in_progress"),
    ]);
    expect(result).toContain("[ ] pending task");
    expect(result).toContain("[~] active task");
  });

  it("includes a header indicating agent-owned checklist", () => {
    const result = formatTodoPrompt([item("1", "task")]);
    expect(result).toMatch(/CURRENT TASKS/);
  });
});
