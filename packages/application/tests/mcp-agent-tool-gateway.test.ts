import { describe, expect, it } from "vitest";
import { McpAgentToolGateway } from "../src/index.js";

describe("McpAgentToolGateway", () => {
  it("exposes bounded MCP discovery and resource context meta-tools", async () => {
    const runtime = {
      listPlugins: async () => [],
      listTools: async () => [{ name: "createNote", description: "Create a note", inputSchema: { type: "object", properties: { text: { type: "string" } } } }],
      startPlugin: async () => ({ pluginId: "com.example.notes", state: "running" }),
      stopPlugin: async () => ({ pluginId: "com.example.notes", state: "idle" }),
      callTool: async () => ({ ok: true }),
      listResources: async () => [{ uri: "notes://daily", name: "Daily notes", mimeType: "text/markdown" }],
      readResource: async () => ({ contents: [{ uri: "notes://daily", mimeType: "text/markdown", text: "# Today" }] }),
    };
    const gateway = new McpAgentToolGateway(runtime as never);

    expect((await gateway.listTools([])).map((tool) => tool.name)).toEqual([
      "mcp_list", "mcp_enable", "mcp_disable", "tool_search", "tool_schema", "resource_search", "resource_read",
    ]);
    await expect(gateway.execute("resource_search", { pluginId: "com.example.notes", query: "daily" }, "call-resource-search")).resolves.toEqual([
      { uri: "notes://daily", name: "Daily notes", mimeType: "text/markdown" },
    ]);
    await expect(gateway.execute("resource_read", { pluginId: "com.example.notes", uri: "notes://daily" }, "call-resource-read")).resolves.toEqual({
      contents: [{ uri: "notes://daily", mimeType: "text/markdown", text: "# Today" }],
    });
    const grant = await gateway.execute("tool_schema", { pluginId: "com.example.notes", toolName: "createNote" }, "call-1") as { name: string };
    expect((await gateway.listTools([])).map((tool) => tool.name)).toContain(grant.name);
    expect(await gateway.execute(grant.name, { text: "hello" }, "call-2")).toEqual({ ok: true });
  });
});
