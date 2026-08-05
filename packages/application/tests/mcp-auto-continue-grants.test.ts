import { describe, expect, it } from "vitest";
import { McpAgentToolGateway } from "../src/index.js";

/**
 * Progressive MCP grant scopes across an outer auto-continue boundary.
 *
 * Auto-continue = new agent.run = new turnId. The gateway today scopes
 * advertised grants to that turnId and clears them on endTurn. These tests pin
 * that contract as a continue-shaped story (user turn → seal → continue turn),
 * not only as isolated turn-1 vs turn-2 smoke.
 */

const TOOL_A = "createNote";
const TOOL_B = "searchFiles";
const TOOL_C = "deleteNote";

const NAME_A = "mcp_nusashell_notes_createNote";
const NAME_B = "mcp_nusashell_files_searchFiles";
const NAME_C = "mcp_nusashell_notes_deleteNote";

function multiPluginRuntime(overrides: { notesState?: string } = {}) {
  const calls: Array<{ pluginId: string; toolName: string }> = [];
  let notesState = overrides.notesState ?? "running";
  return {
    calls,
    setNotesState(state: string) { notesState = state; },
    runtime: {
      listPlugins: async () => [
        { pluginId: "nusashell.notes", name: "Notes", state: notesState, enabled: true },
        { pluginId: "nusashell.files", name: "Files", state: "running", enabled: true },
      ],
      listTools: async (pluginId: { value?: string } | string) => {
        const id = typeof pluginId === "string" ? pluginId : String(pluginId);
        if (id.includes("files")) {
          return [
            { name: TOOL_B, description: "Search files", inputSchema: { type: "object", properties: { query: { type: "string" } } } },
          ];
        }
        return [
          { name: TOOL_A, description: "Create a note", inputSchema: { type: "object", properties: { text: { type: "string" } } } },
          { name: TOOL_C, description: "Delete a note", inputSchema: { type: "object", properties: { id: { type: "string" } } } },
        ];
      },
      startPlugin: async () => ({ pluginId: "nusashell.notes", state: "running" }),
      stopPlugin: async () => ({ pluginId: "nusashell.notes", state: "idle" }),
      callTool: async (
        pluginId: { value?: string } | string,
        options: { toolName: string; args: Readonly<Record<string, unknown>> },
      ) => {
        const id = typeof pluginId === "string" ? pluginId : String(pluginId);
        calls.push({ pluginId: id, toolName: options.toolName });
        return { ok: true, tool: options.toolName, args: options.args };
      },
      listPrompts: async () => [],
      getPrompt: async () => ({ messages: [] }),
      listResources: async () => [],
      listResourceTemplates: async () => [],
      complete: async () => ({ values: [], total: 0, hasMore: false }),
      readResource: async () => ({ contents: [] }),
    },
  };
}

async function grant(gateway: McpAgentToolGateway, pluginId: string, toolName: string, requestId: string, turnId: string): Promise<string> {
  const result = await gateway.execute("tool_schema", { pluginId, toolName }, requestId, turnId) as { name: string };
  return result.name;
}

function advertisedNames(tools: readonly { name: string }[]): string[] {
  return tools.map((t) => t.name).filter((name) => name.startsWith("mcp_nusashell_"));
}

describe("progressive tools across auto-continue turns", () => {
  it("user turn auto-advertises all running tools (A, B, C); grants are idempotent; all execute", async () => {
    const { runtime, calls } = multiPluginRuntime();
    const gateway = new McpAgentToolGateway(runtime as never);

    // Outer auto-continue story: first agent.run after the user message.
    const userTurn = "turn-user";
    gateway.beginTurn(userTurn, { conversationId: "conv-continue-1" });

    const grantA = await grant(gateway, "nusashell.notes", TOOL_A, "schema-a", userTurn);
    const grantB = await grant(gateway, "nusashell.files", TOOL_B, "schema-b", userTurn);
    expect(grantA).toBe(NAME_A);
    expect(grantB).toBe(NAME_B);

    // All running tools are auto-advertised (full catalog inject).
    const listed = advertisedNames(await gateway.listTools([], userTurn));
    expect(listed.sort()).toEqual([NAME_A, NAME_B, NAME_C].sort());

    await expect(gateway.execute(NAME_A, { text: "x" }, "call-a", userTurn)).resolves.toMatchObject({ ok: true, tool: TOOL_A });
    await expect(gateway.execute(NAME_B, { query: "y" }, "call-b", userTurn)).resolves.toMatchObject({ ok: true, tool: TOOL_B });
    // C is auto-advertised — can call directly without prior grant.
    await expect(gateway.execute(NAME_C, { id: "n1" }, "call-c", userTurn)).resolves.toMatchObject({ ok: true, tool: TOOL_C });

    const snap = await gateway.getMcpLiveSnapshot(userTurn);
    expect(snap.tools.map((t) => t.providerName).sort()).toEqual([NAME_A, NAME_B, NAME_C].sort());

    expect(calls.map((c) => c.toolName).sort()).toEqual([TOOL_A, TOOL_B, TOOL_C].sort());
  });

  it("after seal (endTurn) the auto-continue turn auto-advertises all running tools again", async () => {
    const { runtime } = multiPluginRuntime();
    const gateway = new McpAgentToolGateway(runtime as never);

    const userTurn = "turn-user";
    const continueTurn = "turn-continue-1";
    gateway.beginTurn(userTurn, { conversationId: "conv-continue-2" });
    await grant(gateway, "nusashell.notes", TOOL_A, "schema-a", userTurn);
    await grant(gateway, "nusashell.files", TOOL_B, "schema-b", userTurn);
    expect(advertisedNames(await gateway.listTools([], userTurn)).sort()).toEqual([NAME_A, NAME_B, NAME_C].sort());

    // Seal end of successful user turn before desktop starts auto-continue.
    gateway.endTurn(userTurn);

    // New agent.run / autoContinueIndex > 0 → new turnId, same conversation.
    // Auto-seed: all running tools are re-advertised from the live runtime.
    gateway.beginTurn(continueTurn, { conversationId: "conv-continue-2" });

    const listed = advertisedNames(await gateway.listTools([], continueTurn)).sort();
    expect(listed).toEqual([NAME_A, NAME_B, NAME_C].sort());

    const snap = await gateway.getMcpLiveSnapshot(continueTurn);
    expect(snap.tools.map((t) => t.providerName).sort()).toEqual([NAME_A, NAME_B, NAME_C].sort());
    // Plugins still running — runtime unaffected.
    expect(snap.running.map((p) => p.pluginId).sort()).toEqual(["nusashell.files", "nusashell.notes"]);
  });

  it("continue turn auto-advertises all running tools; explicit grant is idempotent", async () => {
    const { runtime, calls } = multiPluginRuntime();
    const gateway = new McpAgentToolGateway(runtime as never);

    const userTurn = "turn-user";
    const continueTurn = "turn-continue-c";
    gateway.beginTurn(userTurn, { conversationId: "conv-continue-3" });
    await grant(gateway, "nusashell.notes", TOOL_A, "schema-a", userTurn);
    await grant(gateway, "nusashell.files", TOOL_B, "schema-b", userTurn);
    await gateway.execute(NAME_A, { text: "x" }, "call-a", userTurn);
    await gateway.execute(NAME_B, { query: "y" }, "call-b", userTurn);
    gateway.endTurn(userTurn);

    // Auto-seed: all running tools advertised.
    gateway.beginTurn(continueTurn, { conversationId: "conv-continue-3" });
    expect(advertisedNames(await gateway.listTools([], continueTurn)).sort()).toEqual([NAME_A, NAME_B, NAME_C].sort());

    // Grant C on top — idempotent (already auto-seeded).
    const grantC = await grant(gateway, "nusashell.notes", TOOL_C, "schema-c", continueTurn);
    expect(grantC).toBe(NAME_C);

    const listed = advertisedNames(await gateway.listTools([], continueTurn)).sort();
    expect(listed).toEqual([NAME_A, NAME_B, NAME_C].sort());

    await expect(gateway.execute(NAME_C, { id: "n1" }, "call-c", continueTurn)).resolves.toMatchObject({
      ok: true,
      tool: TOOL_C,
    });
    await expect(gateway.execute(NAME_A, { text: "again" }, "call-a-sticky", continueTurn)).resolves.toMatchObject({
      ok: true,
      tool: TOOL_A,
    });
    expect(advertisedNames(await gateway.listTools([], continueTurn)).sort()).toEqual(
      [NAME_A, NAME_B, NAME_C].sort(),
    );

    expect(calls.map((c) => c.toolName)).toEqual([TOOL_A, TOOL_B, TOOL_C, TOOL_A]);
  });

  it("sticky grants keep tools from stopped plugins advertised across auto-continue", async () => {
    const { runtime, setNotesState } = multiPluginRuntime();
    const gateway = new McpAgentToolGateway(runtime as never);
    const cid = "conv-sticky-stopped";

    // Turn 1: both plugins running, grant A.
    gateway.beginTurn("turn-1", { conversationId: cid });
    await grant(gateway, "nusashell.notes", TOOL_A, "a", "turn-1");
    gateway.endTurn("turn-1");

    // Simulate notes plugin stopping between turns.
    setNotesState("idle");

    // Turn 2: notes is idle, but A was sticky-granted in the conversation.
    gateway.beginTurn("turn-2", { conversationId: cid });
    const listed = advertisedNames(await gateway.listTools([], "turn-2")).sort();
    // B is auto-advertised (files running); A is sticky (notes stopped but
    // was granted in the conversation). C is NOT auto-advertised (notes idle)
    // and was NOT sticky-granted.
    expect(listed).toContain(NAME_A);
    expect(listed).toContain(NAME_B);
    expect(listed).not.toContain(NAME_C);
  });

  it("endConversation clears sticky grants so stopped-plugin tools disappear", async () => {
    const { runtime, setNotesState } = multiPluginRuntime();
    const gateway = new McpAgentToolGateway(runtime as never);
    const cid = "conv-sticky-cleanup";

    gateway.beginTurn("turn-1", { conversationId: cid });
    await grant(gateway, "nusashell.notes", TOOL_A, "a", "turn-1");
    gateway.endTurn("turn-1");
    gateway.endConversation(cid);

    // Simulate notes stopping.
    setNotesState("idle");
    gateway.beginTurn("turn-2", { conversationId: cid });
    const listed = advertisedNames(await gateway.listTools([], "turn-2")).sort();
    // A is gone (sticky cleared + notes idle). B is still auto-advertised.
    expect(listed).toEqual([NAME_B]);
  });

  it("sticky grants do not leak across different conversations", async () => {
    const { runtime, setNotesState } = multiPluginRuntime();
    const gateway = new McpAgentToolGateway(runtime as never);

    gateway.beginTurn("turn-1", { conversationId: "conv-a" });
    await grant(gateway, "nusashell.notes", TOOL_A, "a", "turn-1");
    gateway.endTurn("turn-1");

    // conv-b: notes stopped so A is not auto-advertised; and not sticky
    // (different conversation) so A should not appear.
    setNotesState("idle");
    gateway.beginTurn("turn-2", { conversationId: "conv-b" });
    expect(advertisedNames(await gateway.listTools([], "turn-2")).sort()).toEqual([NAME_B]);
  });
});
