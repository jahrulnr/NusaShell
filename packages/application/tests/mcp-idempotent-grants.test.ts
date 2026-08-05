import { describe, expect, it, vi } from "vitest";
import { McpAgentToolGateway } from "../src/index.js";

/**
 * Idempotent trust signal for mcp_enable / tool_schema / tool_schemas.
 * The model should not double-enable a running plugin or double-grant a
 * route; the gateway returns the current state with an `alreadyRunning` /
 * `alreadyGranted` flag instead of re-spawning or overwriting.
 */

const TOOL_A = "createNote";
const NAME_A = "mcp_nusashell_notes_createNote";

function runtimeWithStartTracking(initialState: "running" | "idle" = "idle") {
  const calls = { startPlugin: 0, listTools: 0 };
  let state = initialState;
  return {
    calls,
    setState(next: "running" | "idle") { state = next; },
    runtime: {
      listPlugins: async () => [
        { pluginId: "nusashell.notes", name: "Notes", state, enabled: true },
      ],
      listTools: async () => [
        { name: TOOL_A, description: "Create a note", inputSchema: { type: "object", properties: { text: { type: "string" } } } },
      ],
      startPlugin: async () => { calls.startPlugin += 1; state = "running"; return { pluginId: "nusashell.notes", state: "running" }; },
      stopPlugin: async () => { state = "idle"; return { pluginId: "nusashell.notes", state: "idle" }; },
      callTool: async () => ({ ok: true }),
      listPrompts: async () => [],
      getPrompt: async () => ({ messages: [] }),
      listResources: async () => [],
      listResourceTemplates: async () => [],
      complete: async () => ({ values: [], total: 0, hasMore: false }),
      readResource: async () => ({ contents: [] }),
    },
  };
}

describe("mcp_enable idempotency", () => {
  it("does not call startPlugin when plugin is already running; returns alreadyRunning", async () => {
    const { calls, runtime } = runtimeWithStartTracking("running");
    const gateway = new McpAgentToolGateway(runtime as never);
    gateway.beginTurn("turn-already-running");

    const result = await gateway.execute("mcp_enable", { pluginId: "nusashell.notes" }, "r1", "turn-already-running") as {
      pluginId: string; state: string; alreadyRunning?: boolean;
    };

    expect(calls.startPlugin).toBe(0);
    expect(result.state).toBe("running");
    expect(result.alreadyRunning).toBe(true);
  });

  it("calls startPlugin when plugin is idle; returns state running without alreadyRunning", async () => {
    const { calls, runtime } = runtimeWithStartTracking("idle");
    const gateway = new McpAgentToolGateway(runtime as never);
    gateway.beginTurn("turn-idle");

    const result = await gateway.execute("mcp_enable", { pluginId: "nusashell.notes" }, "r1", "turn-idle") as {
      pluginId: string; state: string; alreadyRunning?: boolean;
    };

    expect(calls.startPlugin).toBe(1);
    expect(result.state).toBe("running");
    expect(result.alreadyRunning).toBeUndefined();
  });
});

describe("tool_schema idempotency", () => {
  it("does not overwrite an existing route; returns alreadyGranted", async () => {
    const { runtime } = runtimeWithStartTracking("running");
    const gateway = new McpAgentToolGateway(runtime as never);
    gateway.beginTurn("turn-grant");

    const first = await gateway.execute("tool_schema", { pluginId: "nusashell.notes", toolName: TOOL_A }, "r1", "turn-grant") as { name: string; alreadyGranted?: boolean };
    const second = await gateway.execute("tool_schema", { pluginId: "nusashell.notes", toolName: TOOL_A }, "r2", "turn-grant") as { name: string; alreadyGranted?: boolean };

    expect(first.name).toBe(NAME_A);
    expect(first.alreadyGranted).toBeUndefined();
    expect(second.name).toBe(NAME_A);
    expect(second.alreadyGranted).toBe(true);
  });
});

describe("tool_schemas idempotency", () => {
  it("marks already-granted tools in the result without re-fetching schema", async () => {
    const { runtime } = runtimeWithStartTracking("running");
    const gateway = new McpAgentToolGateway(runtime as never);
    gateway.beginTurn("turn-multi-grant");

    // Grant A first.
    await gateway.execute("tool_schema", { pluginId: "nusashell.notes", toolName: TOOL_A }, "r1", "turn-multi-grant");
    // Re-grant A via tool_schemas — should be marked alreadyGranted.
    const result = await gateway.execute("tool_schemas", { pluginId: "nusashell.notes", toolNames: [TOOL_A] }, "r2", "turn-multi-grant") as {
      granted: Array<{ name: string; alreadyGranted?: boolean }>;
    };

    expect(result.granted).toHaveLength(1);
    expect(result.granted[0].name).toBe(NAME_A);
    expect(result.granted[0].alreadyGranted).toBe(true);
  });
});

describe("live state trust signal in tool result", () => {
  it("mcp_enable response includes a one-line liveState summary (running + granted)", async () => {
    const { runtime } = runtimeWithStartTracking("idle");
    const gateway = new McpAgentToolGateway(runtime as never);
    gateway.beginTurn("turn-live-enable");

    const result = await gateway.execute("mcp_enable", { pluginId: "nusashell.notes" }, "r1", "turn-live-enable") as {
      pluginId: string; state: string; liveState?: string;
    };

    expect(result.state).toBe("running");
    expect(typeof result.liveState).toBe("string");
    expect(result.liveState).toContain("running");
  });

  it("tool_schema response includes a one-line liveState summary after grant", async () => {
    const { runtime } = runtimeWithStartTracking("running");
    const gateway = new McpAgentToolGateway(runtime as never);
    gateway.beginTurn("turn-live-grant");

    const result = await gateway.execute("tool_schema", { pluginId: "nusashell.notes", toolName: TOOL_A }, "r1", "turn-live-grant") as {
      name: string; liveState?: string;
    };

    expect(result.name).toBe(NAME_A);
    expect(typeof result.liveState).toBe("string");
    expect(result.liveState).toContain(NAME_A);
  });

  it("tool_schemas response includes a one-line liveState summary after grant", async () => {
    const { runtime } = runtimeWithStartTracking("running");
    const gateway = new McpAgentToolGateway(runtime as never);
    gateway.beginTurn("turn-live-multi-grant");

    const result = await gateway.execute("tool_schemas", { pluginId: "nusashell.notes", toolNames: [TOOL_A] }, "r1", "turn-live-multi-grant") as {
      granted: Array<{ name: string }>; liveState?: string;
    };

    expect(result.granted).toHaveLength(1);
    expect(typeof result.liveState).toBe("string");
    expect(result.liveState).toContain(NAME_A);
  });
});
