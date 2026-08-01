import { describe, it, expect, vi } from "vitest";
import { ReviewAgentToolGateway } from "../src/agent/services/review-agent-tool-gateway.js";
import type { McpAgentToolGateway } from "../src/agent/services/mcp-agent-tool-gateway.js";
import type { AgentToolDefinition } from "../src/agent/ports/agent-provider.port.js";

function makeFakeInner(): McpAgentToolGateway {
  const allTools: AgentToolDefinition[] = [
    { name: "memory", description: "memory", inputSchema: { type: "object" } },
    { name: "skill_list", description: "list", inputSchema: { type: "object" } },
    { name: "skill_search", description: "search", inputSchema: { type: "object" } },
    { name: "skill_read", description: "read", inputSchema: { type: "object" } },
    { name: "skill_manage", description: "manage", inputSchema: { type: "object" } },
    { name: "some_mcp_tool", description: "mcp", inputSchema: { type: "object" } },
    { name: "another_plugin_tool", description: "plugin", inputSchema: { type: "object" } },
  ];
  return {
    beginTurn: vi.fn(),
    endTurn: vi.fn(),
    cancelTurn: vi.fn(),
    listTools: vi.fn(async () => allTools),
    execute: vi.fn(async (name: string) => ({ ok: true, name })),
    getWriteOrigin: vi.fn(() => "foreground"),
    setWriteOrigin: vi.fn(),
  } as unknown as McpAgentToolGateway;
}

describe("ReviewAgentToolGateway", () => {
  it("listTools filters to whitelist only", async () => {
    const inner = makeFakeInner();
    const gateway = new ReviewAgentToolGateway(inner);
    const tools = await gateway.listTools([], "turn-1");
    const names = tools.map((t) => t.name);
    expect(names).toContain("memory");
    expect(names).toContain("skill_list");
    expect(names).toContain("skill_search");
    expect(names).toContain("skill_read");
    expect(names).toContain("skill_manage");
    expect(names).not.toContain("some_mcp_tool");
    expect(names).not.toContain("another_plugin_tool");
  });

  it("execute allows whitelisted tools", async () => {
    const inner = makeFakeInner();
    const gateway = new ReviewAgentToolGateway(inner);
    const result = await gateway.execute("memory", { key: "test" }, "req-1", "turn-1");
    expect(result).toEqual({ ok: true, name: "memory" });
  });

  it("execute denies non-whitelisted tools", async () => {
    const inner = makeFakeInner();
    const gateway = new ReviewAgentToolGateway(inner);
    await expect(gateway.execute("some_mcp_tool", {}, "req-1", "turn-1")).rejects.toThrow(
      "not allowed in background review",
    );
  });

  it("execute sets writeOrigin to background_review during call", async () => {
    const inner = makeFakeInner();
    (inner as unknown as { getWriteOrigin: ReturnType<typeof vi.fn> }).getWriteOrigin.mockReturnValue("foreground");
    const gateway = new ReviewAgentToolGateway(inner);
    await gateway.execute("memory", {}, "req-1", "turn-1");
    expect(inner.setWriteOrigin).toHaveBeenCalledWith("background_review");
    expect(inner.setWriteOrigin).toHaveBeenCalledWith("foreground");
  });

  it("delegates beginTurn/endTurn/cancelTurn to inner", () => {
    const inner = makeFakeInner();
    const gateway = new ReviewAgentToolGateway(inner);
    gateway.beginTurn("turn-1");
    gateway.endTurn("turn-1");
    void gateway.cancelTurn("turn-1");
    expect(inner.beginTurn).toHaveBeenCalledWith("turn-1", undefined);
    expect(inner.endTurn).toHaveBeenCalledWith("turn-1");
    expect(inner.cancelTurn).toHaveBeenCalledWith("turn-1");
  });
});
