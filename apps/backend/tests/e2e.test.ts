import { describe, expect, it, afterAll, beforeAll } from "vitest";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createContainer } from "../src/container.js";
import { NusaClient } from "@nusashell/plugin-sdk";

const __dirname = dirname(fileURLToPath(import.meta.url));
const PLUGINS_ROOT = resolve(__dirname, "../../../plugins/examples");
const PORT = 9140;

describe("E2E: notes plugin", () => {
  let container: ReturnType<typeof createContainer>;
  let client: NusaClient;

  beforeAll(async () => {
    container = createContainer({
      port: PORT,
      host: "127.0.0.1",
      pluginsRoot: PLUGINS_ROOT,
      ai: { providerId: "stub", stubEnabled: true, maxToolRounds: 8 },
    });
    await container.wsServer.start();

    client = new NusaClient({ url: `ws://127.0.0.1:${PORT}`, defaultTimeoutMs: 15000 });
    await client.connect();
    await client.subscribe();
  });

  afterAll(async () => {
    if (client) await client.disconnect();
    if (container) await container.wsServer.stop();
  });

  it("lists the notes plugin", async () => {
    const result = await client.plugins.list();
    const notes = result.plugins.find((plugin) => plugin.pluginId === "com.example.notes");
    expect(notes).toMatchObject({
      pluginId: "com.example.notes",
      name: "Notes",
      state: "idle",
    });
  });

  it("runs a bounded offline agent turn through the WebSocket command", async () => {
    const result = await client.agent.run([{ role: "user", content: "Hello agent" }]);

    expect(result.text).toBe("(stub) received: Hello agent");
    expect(result.rounds).toBe(1);
    expect(result.toolCalls).toEqual([]);
    expect(result.traceId).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("starts the notes plugin", async () => {
    const result = await client.plugins.start("com.example.notes");
    expect(result.pluginId).toBe("com.example.notes");
    expect(result.state).toBe("running");
  });

  it("runs the stub agent with a running plugin explicitly scoped", async () => {
    const result = await client.agent.run(
      [{ role: "user", content: "Confirm the selected MCP scope" }],
      { pluginIds: ["com.example.notes"] },
    );

    expect(result.text).toBe("(stub) received: Confirm the selected MCP scope");
    expect(result.rounds).toBe(1);
  });

  it("receives plugin.started event", async () => {
    let received = false;
    const unsub = client.on<{ pluginId: string; state: string }>(
      "plugin.started",
      (payload) => {
        if (payload.pluginId === "com.example.notes") {
          received = true;
        }
      },
    );

    // Event may have already been published during start.
    // Re-start to capture it.
    await client.plugins.stop("com.example.notes");
    await client.plugins.start("com.example.notes");

    await new Promise((resolve) => setTimeout(resolve, 100));
    unsub();
    expect(received).toBe(true);
  });

  it("calls createNote tool", async () => {
    const result = await client.tools.call(
      "com.example.notes",
      "00000000-0000-1000-8000-000000000001",
      "createNote",
      { text: "Hello from E2E" },
    );

    expect(result.requestId).toBe("00000000-0000-1000-8000-000000000001");
    const content = result.result as Array<{ type: string; text: string }>;
    expect(content).toHaveLength(1);
    const parsed = JSON.parse(content[0]!.text);
    expect(parsed.note.text).toBe("Hello from E2E");
    expect(parsed.totalNotes).toBeGreaterThanOrEqual(1);
  });

  it("calls listNotes tool", async () => {
    const result = await client.tools.call(
      "com.example.notes",
      "00000000-0000-1000-8000-000000000002",
      "listNotes",
      {},
    );

    expect(result.requestId).toBe("00000000-0000-1000-8000-000000000002");
    const content = result.result as Array<{ type: string; text: string }>;
    expect(content).toHaveLength(1);
    const parsed = JSON.parse(content[0]!.text);
    expect(parsed.notes).toBeInstanceOf(Array);
    expect(parsed.notes.length).toBeGreaterThanOrEqual(1);
  });

  it("stops the notes plugin", async () => {
    const result = await client.plugins.stop("com.example.notes");
    expect(result.pluginId).toBe("com.example.notes");
    expect(result.state).toBe("idle");
  });

  it("gets single plugin details", async () => {
    const result = await client.plugins.get("com.example.notes");
    expect(result.pluginId).toBe("com.example.notes");
    expect(result.name).toBe("Notes");
    expect(result.version).toBe("1.0.0");
    expect(result.icon).toBe("📝");
    expect(result.state).toBe("idle");
    expect(result.enabled).toBe(true);
  });

  it("gets plugin state", async () => {
    const result = await client.plugins.getState("com.example.notes");
    expect(result.pluginId).toBe("com.example.notes");
    expect(result.state).toBe("idle");
  });

  it("restarts the notes plugin", async () => {
    const result = await client.plugins.restart("com.example.notes");
    expect(result.pluginId).toBe("com.example.notes");
    expect(result.state).toBe("running");
  });

  it("lists tools from running plugin", async () => {
    const result = await client.tools.list("com.example.notes");
    expect(result.tools).toHaveLength(2);
    const names = result.tools.map((t) => t.name).sort();
    expect(names).toEqual(["createNote", "listNotes"]);
  });

  it("brokers Notes prompts and resources through the SDK", async () => {
    const prompts = await client.mcp.listPrompts("com.example.notes");
    expect(prompts.prompts.map((prompt) => prompt.name)).toEqual(["summarize_notes"]);

    const prompt = await client.mcp.getPrompt("com.example.notes", "summarize_notes") as { messages: Array<{ content: { text: string } }> };
    expect(prompt.messages[0]!.content.text).toContain("attached Notes MCP resource");

    const resources = await client.mcp.listResources("com.example.notes");
    expect(resources.resources.map((resource) => resource.uri)).toEqual(["notes://all"]);

    const read = await client.mcp.readResource("com.example.notes", "notes://all") as { contents: Array<{ text: string }> };
    expect(JSON.parse(read.contents[0]!.text).notes).toBeInstanceOf(Array);
  });

  it("rejects tool.list when plugin is not running", async () => {
    await client.plugins.stop("com.example.notes");
    await expect(client.tools.list("com.example.notes")).rejects.toThrow();
  });
});
