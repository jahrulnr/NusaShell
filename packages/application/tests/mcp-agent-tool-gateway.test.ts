import { describe, expect, it } from "vitest";
import { McpAgentToolGateway } from "../src/index.js";

describe("McpAgentToolGateway", () => {
  it("exposes bounded MCP discovery and one prompt/resource context meta-tool", async () => {
    const runtime = {
      listPlugins: async () => [],
      listTools: async () => [{ name: "createNote", description: "Create a note", inputSchema: { type: "object", properties: { text: { type: "string" } } } }],
      startPlugin: async () => ({ pluginId: "com.example.notes", state: "running" }),
      stopPlugin: async () => ({ pluginId: "com.example.notes", state: "idle" }),
      callTool: async () => ({ ok: true }),
      listPrompts: async () => [{ name: "daily", description: "Daily prompt" }],
      getPrompt: async () => ({ messages: [{ role: "user", content: { type: "text", text: "Review today" } }] }),
      listResources: async () => [{ uri: "notes://daily", name: "Daily notes", mimeType: "text/markdown" }],
      listResourceTemplates: async () => [{ uriTemplate: "notes://{date}", name: "Notes by date", mimeType: "text/markdown" }],
      complete: async () => ({ values: ["2026-07-29"], total: 1, hasMore: false }),
      readResource: async () => ({ contents: [{ uri: "notes://daily", mimeType: "text/markdown", text: "# Today" }] }),
    };
    const gateway = new McpAgentToolGateway(runtime as never);
    gateway.beginTurn("turn-1");

    expect((await gateway.listTools([], "turn-1")).map((tool) => tool.name)).toEqual([
      "mcp_list", "mcp_enable", "mcp_disable", "tool_search", "tool_list", "tool_schema", "mcp_context",
    ]);
    await expect(gateway.execute("tool_list", { pluginId: "com.example.notes" }, "call-tool-list", "turn-1")).resolves.toEqual([
      { name: "createNote", description: "Create a note" },
    ]);
    await expect(gateway.execute("mcp_context", { pluginId: "com.example.notes", action: "list_prompts", query: "daily" }, "call-prompt-list", "turn-1")).resolves.toEqual([
      { name: "daily", description: "Daily prompt" },
    ]);
    await expect(gateway.execute("mcp_context", { pluginId: "com.example.notes", action: "get_prompt", name: "daily", arguments: {} }, "call-prompt-get", "turn-1")).resolves.toEqual({
      messages: [{ role: "user", content: { type: "text", text: "Review today" } }],
    });
    await expect(gateway.execute("mcp_context", { pluginId: "com.example.notes", action: "search_resources", query: "daily" }, "call-resource-search", "turn-1")).resolves.toEqual([
      { uri: "notes://daily", name: "Daily notes", mimeType: "text/markdown" },
    ]);
    await expect(gateway.execute("mcp_context", { pluginId: "com.example.notes", action: "list_resource_templates", query: "date" }, "call-resource-template", "turn-1")).resolves.toEqual([
      { uriTemplate: "notes://{date}", name: "Notes by date", mimeType: "text/markdown" },
    ]);
    await expect(gateway.execute("mcp_context", {
      pluginId: "com.example.notes",
      action: "complete",
      refType: "resource",
      uri: "notes://{date}",
      argumentName: "date",
      argumentValue: "2026-07",
      arguments: {},
    }, "call-completion", "turn-1")).resolves.toEqual({
      values: ["2026-07-29"],
      total: 1,
      hasMore: false,
    });
    await expect(gateway.execute("mcp_context", { pluginId: "com.example.notes", action: "read_resource", uri: "notes://daily" }, "call-resource-read", "turn-1")).resolves.toEqual({
      contents: [{ uri: "notes://daily", mimeType: "text/markdown", text: "# Today" }],
    });
    const grant = await gateway.execute("tool_schema", { pluginId: "com.example.notes", toolName: "createNote" }, "call-1", "turn-1") as { name: string };
    expect((await gateway.listTools([], "turn-1")).map((tool) => tool.name)).toContain(grant.name);
    expect((await gateway.listTools([], "turn-2")).map((tool) => tool.name)).not.toContain(grant.name);
    expect(await gateway.execute(grant.name, { text: "hello" }, "call-2", "turn-1")).toEqual({ ok: true });
    await expect(gateway.execute(grant.name, { text: "hello" }, "call-3", "turn-2")).rejects.toThrow("outside the MCP allowlist");
    gateway.endTurn("turn-1");
    expect((await gateway.listTools([], "turn-1")).map((tool) => tool.name)).not.toContain(grant.name);
  });
});
