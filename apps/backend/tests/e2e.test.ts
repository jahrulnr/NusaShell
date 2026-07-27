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
    container = createContainer({ port: PORT, host: "127.0.0.1", pluginsRoot: PLUGINS_ROOT });
    await container.wsServer.start();

    client = new NusaClient({ url: `ws://127.0.0.1:${PORT}`, defaultTimeoutMs: 15000 });
    await client.connect();
    await client.request("subscribe", { eventTypes: ["*"] });
  });

  afterAll(async () => {
    if (client) await client.disconnect();
    if (container) await container.wsServer.stop();
  });

  it("lists the notes plugin", async () => {
    const result = await client.plugins.list();
    expect(result.plugins).toHaveLength(1);
    expect(result.plugins[0]!.pluginId).toBe("com.example.notes");
    expect(result.plugins[0]!.name).toBe("Notes");
    expect(result.plugins[0]!.state).toBe("idle");
  });

  it("starts the notes plugin", async () => {
    const result = await client.plugins.start("com.example.notes");
    expect(result.pluginId).toBe("com.example.notes");
    expect(result.state).toBe("running");
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

  it("rejects tool.list when plugin is not running", async () => {
    await client.plugins.stop("com.example.notes");
    await expect(client.tools.list("com.example.notes")).rejects.toThrow();
  });
});
