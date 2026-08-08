import { describe, expect, it } from "vitest";
import { McpAgentToolGateway } from "../src/index.js";

const fakeRuntime = (plugins: readonly { pluginId: string; state: string }[]) => ({
  listPlugins: async () => plugins,
  listTools: async () => [
    { name: "createNote", description: "Create a note", inputSchema: { type: "object", properties: { text: { type: "string" } } } },
    { name: "searchFiles", description: "Search files", inputSchema: { type: "object", properties: { query: { type: "string" } } } },
  ],
  startPlugin: async () => ({ pluginId: "x", state: "running" }),
  stopPlugin: async () => ({ pluginId: "x", state: "idle" }),
  callTool: async () => ({ ok: true }),
  listPrompts: async () => [],
  getPrompt: async () => ({ messages: [] }),
  listResources: async () => [],
  listResourceTemplates: async () => [],
  complete: async () => ({ values: [], total: 0, hasMore: false }),
  readResource: async () => ({ contents: [] }),
});

describe("McpAgentToolGateway.getMcpLiveSnapshot", () => {
  it("returns running plugins and full tool catalog (name+desc+schema) for running plugins", async () => {
    const runtime = fakeRuntime([
      { pluginId: "nusashell.notes", state: "running" },
      { pluginId: "nusashell.files", state: "running" },
      { pluginId: "nusashell.mail", state: "idle" },
    ]);
    const gateway = new McpAgentToolGateway(runtime as never);
    gateway.beginTurn("turn-live");

    const snap = await gateway.getMcpLiveSnapshot("turn-live");
    const runningIds = snap.running.map((p) => p.pluginId).sort();
    expect(runningIds).toEqual(["nusashell.files", "nusashell.notes"]);
    // Idle plugin (mail) is excluded from running and from tools.
    expect(snap.running.find((p) => p.pluginId === "nusashell.mail")).toBeUndefined();

    // Tools: both running plugins contribute their tools, sorted by providerName.
    const toolNames = snap.tools.map((t) => t.providerName).sort();
    expect(toolNames).toEqual([
      "mcp_nusashell_files_createNote",
      "mcp_nusashell_files_searchFiles",
      "mcp_nusashell_notes_createNote",
      "mcp_nusashell_notes_searchFiles",
    ]);

    // Full fields present.
    const first = snap.tools[0]!;
    expect(first.pluginId).toBeDefined();
    expect(first.toolName).toBeDefined();
    expect(first.description).toBeDefined();
    expect(first.inputSchema).toBeDefined();
    expect(first.inputSchema).toEqual({ type: "object", properties: { text: { type: "string" } } });
  });

  it("returns running plugins even when no grants (tools still populated from enumeration)", async () => {
    const runtime = fakeRuntime([
      { pluginId: "nusashell.notes", state: "running" },
    ]);
    const gateway = new McpAgentToolGateway(runtime as never);
    gateway.beginTurn("turn-no-grants");

    const snap = await gateway.getMcpLiveSnapshot("turn-no-grants");
    expect(snap.running.map((p) => p.pluginId)).toEqual(["nusashell.notes"]);
    expect(snap.tools.length).toBe(2);
    expect(snap.toolsOverflow ?? []).toEqual([]);
  });

  it("returns empty running and empty tools when nothing is running", async () => {
    const runtime = fakeRuntime([]);
    const gateway = new McpAgentToolGateway(runtime as never);
    gateway.beginTurn("turn-empty");

    const snap = await gateway.getMcpLiveSnapshot("turn-empty");
    expect(snap.running).toEqual([]);
    expect(snap.tools).toEqual([]);
    expect(snap.toolsOverflow ?? []).toEqual([]);
  });

  it("fail-soft: listPlugins error returns empty running and empty tools, does not throw", async () => {
    const runtime = {
      ...fakeRuntime([]),
      listPlugins: async () => { throw new Error("runtime unavailable"); },
    };
    const gateway = new McpAgentToolGateway(runtime as never);
    gateway.beginTurn("turn-fail");

    const snap = await gateway.getMcpLiveSnapshot("turn-fail");
    expect(snap.running).toEqual([]);
    expect(snap.tools).toEqual([]);
  });

  it("fail-soft per plugin: listTools error on one plugin skips it, others still contribute", async () => {
    const runtime = {
      ...fakeRuntime([
        { pluginId: "nusashell.notes", state: "running" },
        { pluginId: "nusashell.mail", state: "running" },
      ]),
      listTools: async (pluginId: unknown) => {
        const id = String(pluginId);
        if (id.includes("mail")) throw new Error("mail server crashed");
        return [
          { name: "createNote", description: "Create a note", inputSchema: { type: "object", properties: {} } },
        ];
      },
    };
    const gateway = new McpAgentToolGateway(runtime as never);
    gateway.beginTurn("turn-soft");

    const snap = await gateway.getMcpLiveSnapshot("turn-soft");
    expect(snap.running.map((p) => p.pluginId).sort()).toEqual(["nusashell.mail", "nusashell.notes"]);
    // Only notes contributed; mail was skipped.
    expect(snap.tools.map((t) => t.pluginId)).toEqual(["nusashell.notes"]);
  });

  it("does not start plugins (read-only)", async () => {
    let startCalled = false;
    const runtime = {
      ...fakeRuntime([{ pluginId: "nusashell.notes", state: "running" }]),
      startPlugin: async () => { startCalled = true; return { pluginId: "x", state: "running" }; },
    };
    const gateway = new McpAgentToolGateway(runtime as never);
    gateway.beginTurn("turn-ro");
    await gateway.getMcpLiveSnapshot("turn-ro");
    expect(startCalled).toBe(false);
  });

  it("keeps the full running catalog for the context checkpoint even when provider tools[] is capped", async () => {
    const manyTools = Array.from({ length: 100 }, (_, i) => ({
      name: `tool_${i}`,
      description: `Tool ${i}`,
      inputSchema: { type: "object", properties: {} },
    }));
    const runtime = {
      ...fakeRuntime([{ pluginId: "nusashell.big", state: "running" }]),
      listTools: async () => manyTools,
    };
    const gateway = new McpAgentToolGateway(runtime as never);
    gateway.beginTurn("turn-cap");

    const snap = await gateway.getMcpLiveSnapshot("turn-cap");
    expect(snap.tools.length).toBe(100);
    expect(snap.toolsOverflow ?? []).toEqual([]);
  });
});
