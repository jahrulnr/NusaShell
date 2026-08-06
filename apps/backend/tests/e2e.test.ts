import { describe, expect, it, afterAll, beforeAll } from "vitest";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createContainer } from "../src/container.js";
import { NusaClient, WebSocketConnection } from "@nusashell/plugin-sdk";

const __dirname = dirname(fileURLToPath(import.meta.url));
const PLUGINS_ROOT = resolve(__dirname, "../../../plugins");
const PORT = 9140;
const NOTES_DATA_ENV = "NUSASHELL_NOTES_DATA_FILE";

describe("E2E: notes plugin", () => {
  let container: ReturnType<typeof createContainer>;
  let client: NusaClient;
  let notesDataDir: string;
  let notesDataFile: string;
  let originalNotesDataFile: string | undefined;

  beforeAll(async () => {
    // Isolate Notes MCP persistence away from plugins/notes/notes.json so E2E
    // never pollutes the repo copy that electron-forge packages into installs.
    notesDataDir = await mkdtemp(join(tmpdir(), "nusashell-notes-e2e-"));
    notesDataFile = join(notesDataDir, "notes.json");
    originalNotesDataFile = process.env[NOTES_DATA_ENV];
    process.env[NOTES_DATA_ENV] = notesDataFile;

    container = createContainer({
      port: PORT,
      host: "127.0.0.1",
      pluginsRoot: PLUGINS_ROOT,
      ai: { providerId: "stub", stubEnabled: true, maxToolRounds: 8 },
    });
    await container.wsServer.start();

    client = new NusaClient({ url: `ws://127.0.0.1:${PORT}`, defaultTimeoutMs: 15000, connectionFactory: (url, cb) => new WebSocketConnection(url, cb) });
    await client.connect();
    await client.subscribe();
  });

  afterAll(async () => {
    try {
      if (client) await client.disconnect();
    } finally {
      try {
        if (container) {
          await container.runtimeManager.stopAll();
          await container.wsServer.stop();
        }
      } finally {
        if (originalNotesDataFile === undefined) delete process.env[NOTES_DATA_ENV];
        else process.env[NOTES_DATA_ENV] = originalNotesDataFile;
        if (notesDataDir) await rm(notesDataDir, { recursive: true, force: true });
      }
    }
  });

  it("lists the notes plugin", async () => {
    const result = await client.plugins.list();
    const notes = result.plugins.find((plugin) => plugin.pluginId === "nusashell.notes");
    expect(notes).toMatchObject({
      pluginId: "nusashell.notes",
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
    const result = await client.plugins.start("nusashell.notes");
    expect(result.pluginId).toBe("nusashell.notes");
    expect(result.state).toBe("running");
  });

  it("runs the stub agent with a running plugin explicitly scoped", async () => {
    const result = await client.agent.run(
      [{ role: "user", content: "Confirm the selected MCP scope" }],
      { pluginIds: ["nusashell.notes"] },
    );

    expect(result.text).toBe("(stub) received: Confirm the selected MCP scope");
    expect(result.rounds).toBe(1);
  });

  it("receives plugin.started event", async () => {
    let received = false;
    const unsub = client.on<{ pluginId: string; state: string }>(
      "plugin.started",
      (payload) => {
        if (payload.pluginId === "nusashell.notes") {
          received = true;
        }
      },
    );

    // Event may have already been published during start.
    // Re-start to capture it.
    await client.plugins.stop("nusashell.notes");
    await client.plugins.start("nusashell.notes");

    await new Promise((resolve) => setTimeout(resolve, 100));
    unsub();
    expect(received).toBe(true);
  });

  it("calls create tool", async () => {
    const result = await client.tools.call(
      "nusashell.notes",
      "00000000-0000-1000-8000-000000000001",
      "create",
      { text: "Hello from E2E" },
    );

    expect(result.requestId).toBe("00000000-0000-1000-8000-000000000001");
    const parsed = result.result as { note: { text: string }; totalNotes: number };
    expect(parsed.note.text).toBe("Hello from E2E");
    // Isolated temp store starts empty; proves create did not touch real data.
    expect(parsed.totalNotes).toBe(1);
  });

  it("calls list tool", async () => {
    const result = await client.tools.call(
      "nusashell.notes",
      "00000000-0000-1000-8000-000000000002",
      "list",
      {},
    );

    expect(result.requestId).toBe("00000000-0000-1000-8000-000000000002");
    const parsed = result.result as { notes: Array<{ text: string }>; total: number };
    expect(parsed.notes).toBeInstanceOf(Array);
    expect(parsed.total).toBe(1);
    expect(parsed.notes.map((note) => note.text)).toEqual(["Hello from E2E"]);
  });

  it("stops the notes plugin", async () => {
    const result = await client.plugins.stop("nusashell.notes");
    expect(result.pluginId).toBe("nusashell.notes");
    expect(result.state).toBe("idle");
  });

  it("gets single plugin details", async () => {
    const result = await client.plugins.get("nusashell.notes");
    expect(result.pluginId).toBe("nusashell.notes");
    expect(result.name).toBe("Notes");
    expect(result.version).toBe("1.0.0");
    expect(result.icon).toBe("📝");
    expect(result.state).toBe("idle");
    expect(result.enabled).toBe(true);
  });

  it("gets plugin state", async () => {
    const result = await client.plugins.getState("nusashell.notes");
    expect(result.pluginId).toBe("nusashell.notes");
    expect(result.state).toBe("idle");
  });

  it("restarts the notes plugin", async () => {
    const result = await client.plugins.restart("nusashell.notes");
    expect(result.pluginId).toBe("nusashell.notes");
    expect(result.state).toBe("running");
  });

  it("lists tools from running plugin", async () => {
    const result = await client.tools.list("nusashell.notes");
    expect(result.tools).toHaveLength(6);
    const names = result.tools.map((t) => t.name).sort();
    expect(names).toEqual(["create", "delete", "get", "list", "search", "update"]);
  });

  it("lists plugin-authored prompts while resources remain unsupported", async () => {
    const result = await client.mcp.listPrompts("nusashell.notes");
    expect(result.prompts.map((prompt) => prompt.name)).toEqual(["howto"]);
    const prompt = await client.mcp.getPrompt("nusashell.notes", "howto");
    expect(prompt.messages[0].content.text).toContain("create");
    await expect(client.mcp.listResources("nusashell.notes")).rejects.toThrow();
  });

  it("rejects tool.list when plugin is not running", async () => {
    await client.plugins.stop("nusashell.notes");
    await expect(client.tools.list("nusashell.notes")).rejects.toThrow();
  });
});
