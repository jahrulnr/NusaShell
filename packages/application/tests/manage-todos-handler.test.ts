import { describe, expect, it } from "vitest";
import { ManageTodosHandler, InMemoryConversationTodoPort, type AgentTodoItem } from "../src/index.js";

function item(id: string, content: string, status: AgentTodoItem["status"] = "pending"): AgentTodoItem {
  return { id, content, status };
}

describe("ManageTodosHandler", () => {
  it("set replaces the full list and publishes the event", async () => {
    const port = new InMemoryConversationTodoPort();
    const published: Array<{ conversationId: string; items: AgentTodoItem[] }> = [];
    const handler = new ManageTodosHandler(port, (conversationId, items) => published.push({ conversationId, items: [...items] }));
    const result = await handler.handle({
      kind: "manage-todos",
      conversationId: "conv-1",
      action: "set",
      items: [item("1", "first"), item("2", "second", "in_progress")],
    });
    expect(result.ok).toBe(true);
    expect(result.total).toBe(2);
    expect(port.get("conv-1")).toHaveLength(2);
    expect(published).toHaveLength(1);
    expect(published[0].items).toHaveLength(2);
  });

  it("delete removes items by id and does not leave deleted ids in the port", async () => {
    const port = new InMemoryConversationTodoPort();
    port.set("conv-1", [item("1", "keep"), item("2", "delete"), item("3", "keep too")]);
    const published: Array<{ conversationId: string; items: AgentTodoItem[] }> = [];
    const handler = new ManageTodosHandler(port, (conversationId, items) => published.push({ conversationId, items: [...items] }));
    const result = await handler.handle({
      kind: "manage-todos",
      conversationId: "conv-1",
      action: "delete",
      ids: ["2"],
    });
    expect(result.ok).toBe(true);
    expect(result.total).toBe(2);
    const remaining = port.get("conv-1");
    expect(remaining.map((i) => i.id)).toEqual(["1", "3"]);
    expect(published).toHaveLength(1);
    expect(published[0].items.map((i) => i.id)).toEqual(["1", "3"]);
  });

  it("delete with unknown ids is a no-op but still publishes", async () => {
    const port = new InMemoryConversationTodoPort();
    port.set("conv-1", [item("1", "keep")]);
    const handler = new ManageTodosHandler(port, () => {});
    const result = await handler.handle({
      kind: "manage-todos",
      conversationId: "conv-1",
      action: "delete",
      ids: ["nonexistent"],
    });
    expect(result.ok).toBe(true);
    expect(result.total).toBe(1);
  });

  it("rejects invalid status on set", async () => {
    const port = new InMemoryConversationTodoPort();
    const handler = new ManageTodosHandler(port);
    await expect(handler.handle({
      kind: "manage-todos",
      conversationId: "conv-1",
      action: "set",
      items: [{ id: "1", content: "x", status: "bogus" as AgentTodoItem["status"] }],
    })).rejects.toThrow("status must be");
  });

  it("rejects empty conversationId", async () => {
    const port = new InMemoryConversationTodoPort();
    const handler = new ManageTodosHandler(port);
    await expect(handler.handle({
      kind: "manage-todos",
      conversationId: "",
      action: "set",
      items: [],
    })).rejects.toThrow("conversationId is required");
  });
});
