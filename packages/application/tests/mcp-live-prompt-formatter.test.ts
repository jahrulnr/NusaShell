import { describe, expect, it } from "vitest";
import {
  formatMcpLivePrompt,
  MCP_LIVE_PROMPT_BUDGET_CHARS,
  MCP_LIVE_TOOLS_CAP,
  type McpLiveSnapshot,
  type McpLiveSnapshotTool,
} from "../src/agent/services/mcp-live-prompt-formatter.js";

const tool = (
  providerName: string,
  overrides: Partial<McpLiveSnapshotTool> = {},
): McpLiveSnapshotTool => ({
  providerName,
  pluginId: overrides.pluginId ?? "plugin.a",
  toolName: overrides.toolName ?? providerName.replace(/^mcp_[^_]+_/, ""),
  inputSchema: overrides.inputSchema ?? { type: "object", properties: { text: { type: "string" } } },
  ...(overrides.description ? { description: overrides.description } : {}),
});

const snap = (
  running: readonly string[],
  tools: readonly McpLiveSnapshotTool[] = [],
  overflow: readonly string[] = [],
): McpLiveSnapshot => ({
  running: running.map((pluginId) => ({ pluginId })),
  tools,
  ...(overflow.length > 0 ? { toolsOverflow: overflow } : {}),
});

describe("formatMcpLivePrompt", () => {
  it("returns undefined when no running plugins, no tools, and no overflow", () => {
    expect(formatMcpLivePrompt(snap([], []))).toBeUndefined();
    expect(formatMcpLivePrompt(snap([], [], []))).toBeUndefined();
  });

  it("returns undefined when snapshot is empty regardless of budget", () => {
    expect(formatMcpLivePrompt(snap([], []), 500)).toBeUndefined();
  });

  it("renders running plugins with sorted ids + Live MCP header", () => {
    const out = formatMcpLivePrompt(snap(["plugin.b", "plugin.a"], []));
    expect(out).toBeDefined();
    expect(out).toContain("## Live MCP (runtime)");
    expect(out).toContain("Running: plugin.a, plugin.b");
  });

  it("renders tool name + description + inputSchema JSON for each tool", () => {
    const out = formatMcpLivePrompt(snap(["plugin.a"], [
      tool("mcp_a_foo", { description: "Create a note", inputSchema: { type: "object", properties: { text: { type: "string" } } } }),
    ]));
    expect(out).toBeDefined();
    expect(out).toContain("### mcp_a_foo");
    expect(out).toContain("description: Create a note");
    expect(out).toContain("inputSchema:");
    expect(out).toContain("```json");
    expect(out).toContain('"type": "object"');
    expect(out).toContain('"text"');
    expect(out).toContain('"properties"');
  });

  it("omits description line when tool has no description", () => {
    const out = formatMcpLivePrompt(snap(["plugin.a"], [
      tool("mcp_a_foo"),
    ]));
    expect(out).toBeDefined();
    expect(out).toContain("### mcp_a_foo");
    expect(out).not.toContain("description:");
  });

  it("sorts tools by providerName for prompt-cache stability", () => {
    const out = formatMcpLivePrompt(snap(["plugin.a", "plugin.b"], [
      tool("mcp_b_bar"),
      tool("mcp_a_foo"),
    ]));
    expect(out).toBeDefined();
    const fooIdx = out!.indexOf("### mcp_a_foo");
    const barIdx = out!.indexOf("### mcp_b_bar");
    expect(fooIdx).toBeLessThan(barIdx);
    expect(fooIdx).toBeGreaterThan(0);
  });

  it("includes guidance: prefer these tools, call directly, idle needs mcp_enable", () => {
    const out = formatMcpLivePrompt(snap(["plugin.a"], [tool("mcp_a_foo")]));
    expect(out).toContain("Prefer these tools");
    expect(out).toContain("call provider names directly");
    expect(out).toContain("Idle plugins still need mcp_enable");
  });

  it("lists overflow tool names when toolsOverflow is present", () => {
    const out = formatMcpLivePrompt(snap(["plugin.a"], [
      tool("mcp_a_foo"),
    ], ["mcp_a_bar", "mcp_a_baz"]));
    expect(out).toBeDefined();
    expect(out).toContain("Present but not in tools[]");
    expect(out).toContain("mcp_a_bar, mcp_a_baz");
  });

  it("truncates with explicit tail when over budget and never exceeds cap", () => {
    // Build a snapshot with many tools to exceed a small budget.
    const manyTools = Array.from({ length: 200 }, (_, i) =>
      tool(`mcp_a_tool_${i}`, { description: `Tool ${i} does something useful with a long description`.repeat(5) }),
    );
    const out = formatMcpLivePrompt(snap(["plugin.a"], manyTools), 500);
    expect(out).toBeDefined();
    expect(out!.length).toBeLessThanOrEqual(500);
    expect(out).toContain("truncated");
    expect(out).toContain("tool_list");
  });

  it("budget is 80_000 chars (full catalog, not names-only)", () => {
    expect(MCP_LIVE_PROMPT_BUDGET_CHARS).toBe(80_000);
  });

  it("tools cap is 96", () => {
    expect(MCP_LIVE_TOOLS_CAP).toBe(96);
  });

  it("renders full schema JSON (not names-only)", () => {
    const out = formatMcpLivePrompt(snap(["plugin.a"], [
      tool("mcp_a_foo", { inputSchema: { type: "object", properties: { x: { type: "integer" } }, required: ["x"] } }),
    ]));
    expect(out).toContain('"required"');
    expect(out).toContain('"integer"');
  });
});
