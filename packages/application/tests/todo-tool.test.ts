import { describe, expect, it } from "vitest";
import { McpAgentToolGateway, InMemoryConversationTodoPort, type AgentTodoItem } from "../src/index.js";

const fakeRuntime = {
  listPlugins: async () => [],
  listTools: async () => [],
  startPlugin: async () => ({ pluginId: "x", state: "running" }),
  stopPlugin: async () => ({ pluginId: "x", state: "idle" }),
  callTool: async () => ({ ok: true }),
  listPrompts: async () => [],
  getPrompt: async () => ({ messages: [] }),
  listResources: async () => [],
  listResourceTemplates: async () => [],
  complete: async () => ({ values: [], total: 0, hasMore: false }),
  readResource: async () => ({ contents: [] }),
};

function item(id: string, content: string, status: AgentTodoItem["status"] = "pending"): AgentTodoItem {
  return { id, content, status };
}

describe("todo meta-tool", () => {
  it("advertises the todo tool when a todo port is bound", async () => {
    const gateway = new McpAgentToolGateway(fakeRuntime as never);
    gateway.bindTodos(new InMemoryConversationTodoPort());
    gateway.beginTurn("turn-1", { conversationId: "conv-1" });
    const names = (await gateway.listTools([], "turn-1")).map((t) => t.name);
    expect(names).toContain("todo");
  });

  it("does not advertise todo when no todo port is bound", async () => {
    const gateway = new McpAgentToolGateway(fakeRuntime as never);
    gateway.beginTurn("turn-1");
    const names = (await gateway.listTools([], "turn-1")).map((t) => t.name);
    expect(names).not.toContain("todo");
  });

  it("replaces the full list and returns summary counts", async () => {
    const port = new InMemoryConversationTodoPort();
    const gateway = new McpAgentToolGateway(fakeRuntime as never);
    gateway.bindTodos(port);
    gateway.beginTurn("turn-1", { conversationId: "conv-1" });
    const result = await gateway.execute("todo", {
      items: [
        { id: "1", content: "first", status: "pending" },
        { id: "2", content: "second", status: "in_progress" },
        { id: "3", content: "done", status: "completed" },
      ],
    }, "call-1", "turn-1") as { ok: boolean; total: number; pending: number; inProgress: number; completed: number; items: AgentTodoItem[] };
    expect(result.ok).toBe(true);
    expect(result.total).toBe(3);
    expect(result.pending).toBe(1);
    expect(result.inProgress).toBe(1);
    expect(result.completed).toBe(1);
    expect(port.get("conv-1")).toHaveLength(3);
  });

  it("clears the list when items is empty", async () => {
    const port = new InMemoryConversationTodoPort();
    port.set("conv-1", [item("1", "task")]);
    const gateway = new McpAgentToolGateway(fakeRuntime as never);
    gateway.bindTodos(port);
    gateway.beginTurn("turn-1", { conversationId: "conv-1" });
    const result = await gateway.execute("todo", { items: [] }, "call-1", "turn-1") as { ok: boolean; total: number };
    expect(result.ok).toBe(true);
    expect(result.total).toBe(0);
    expect(port.get("conv-1")).toHaveLength(0);
  });

  it("rejects when conversationId is not set on the turn context", async () => {
    const gateway = new McpAgentToolGateway(fakeRuntime as never);
    gateway.bindTodos(new InMemoryConversationTodoPort());
    gateway.beginTurn("turn-1");
    await expect(gateway.execute("todo", { items: [] }, "call-1", "turn-1")).rejects.toThrow("conversation context");
  });

  it("rejects duplicate item ids", async () => {
    const gateway = new McpAgentToolGateway(fakeRuntime as never);
    gateway.bindTodos(new InMemoryConversationTodoPort());
    gateway.beginTurn("turn-1", { conversationId: "conv-1" });
    await expect(gateway.execute("todo", {
      items: [
        { id: "1", content: "a", status: "pending" },
        { id: "1", content: "b", status: "pending" },
      ],
    }, "call-1", "turn-1")).rejects.toThrow("duplicate");
  });

  it("rejects empty content", async () => {
    const gateway = new McpAgentToolGateway(fakeRuntime as never);
    gateway.bindTodos(new InMemoryConversationTodoPort());
    gateway.beginTurn("turn-1", { conversationId: "conv-1" });
    await expect(gateway.execute("todo", {
      items: [{ id: "1", content: "  ", status: "pending" }],
    }, "call-1", "turn-1")).rejects.toThrow("non-empty content");
  });

  it("publishes agent.todo_updated event via the bound publisher", async () => {
    const port = new InMemoryConversationTodoPort();
    const published: Array<{ conversationId: string; items: AgentTodoItem[] }> = [];
    const gateway = new McpAgentToolGateway(fakeRuntime as never);
    gateway.bindTodos(port, (conversationId, items) => published.push({ conversationId, items: [...items] }));
    gateway.beginTurn("turn-1", { conversationId: "conv-1" });
    await gateway.execute("todo", { items: [{ id: "1", content: "task", status: "pending" }] }, "call-1", "turn-1");
    expect(published).toHaveLength(1);
    expect(published[0]!.conversationId).toBe("conv-1");
    expect(published[0]!.items).toHaveLength(1);
  });
});
