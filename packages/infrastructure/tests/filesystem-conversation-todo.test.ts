import { mkdtemp, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { FilesystemConversationTodoPort } from "../src/agent/filesystem-conversation-todo-port.js";

describe("FilesystemConversationTodoPort", () => {
  it("keeps conversation todos after the runtime is recreated", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-todos-"));
    const first = new FilesystemConversationTodoPort(root);
    const items = [{ id: "task-1", content: "Install safely", status: "in_progress" as const }];

    first.set("conversation-a", items);

    const second = new FilesystemConversationTodoPort(root);
    expect(second.get("conversation-a")).toEqual(items);
  });

  it("atomically persists deletes and clears", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-todos-"));
    const port = new FilesystemConversationTodoPort(root);
    port.set("conversation-a", [{ id: "task-1", content: "Keep", status: "pending" }]);
    port.clear("conversation-a");

    const restored = new FilesystemConversationTodoPort(root);
    expect(restored.get("conversation-a")).toEqual([]);
    expect(await readFile(join(root, "conversation-todos.json"), "utf8")).toBe("{}\n");
  });
});
