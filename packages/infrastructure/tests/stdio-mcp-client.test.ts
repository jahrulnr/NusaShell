import { describe, expect, it, afterEach } from "vitest";
import { McpClientFactory } from "../src/mcp/mcp-client.factory.js";
import { resolveStdioLaunch } from "../src/mcp/stdio-mcp-client.adapter.js";
import type { McpClientPort } from "@nusashell/application";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

const MOCK_SERVER_PATH = resolve(
  fileURLToPath(import.meta.url),
  "..",
  "mock-mcp-server.mjs",
);

describe("StdioMcpClient", () => {
  let client: McpClientPort | null = null;

  it("uses Electron as Node when running inside the packaged desktop", () => {
    const launch = resolveStdioLaunch("node", { PLUGIN_ENV: "yes" }, {
      execPath: "/opt/NusaShell/NusaShell",
      electronVersion: "33.4.11",
    });
    expect(launch.command).toBe("/opt/NusaShell/NusaShell");
    expect(launch.env.ELECTRON_RUN_AS_NODE).toBe("1");
    expect(launch.env.PLUGIN_ENV).toBe("yes");
  });

  it("keeps the manifest command outside Electron", () => {
    const launch = resolveStdioLaunch("node", { PLUGIN_ENV: "yes" }, {
      execPath: "/usr/bin/node",
    });
    expect(launch.command).toBe("node");
    expect(launch.env.PLUGIN_ENV).toBe("yes");
  });

  afterEach(async () => {
    if (client) {
      await client.close();
      client = null;
    }
  });

  it("connects, lists tools, and calls a tool via stdio", async () => {
    const factory = new McpClientFactory();
    client = factory.createForStdio("node", [MOCK_SERVER_PATH], {});

    await client.connect();

    const tools = await client.listTools();
    expect(tools).toHaveLength(1);
    expect(tools[0]!.name).toBe("echo");
    expect(tools[0]!.description).toBe("Echoes the input");

    const result = await client.callTool("echo", { message: "hello" });
    expect(result).toEqual([{ type: "text", text: "hello" }]);
  });

  it("throws when calling listTools before connect", async () => {
    const factory = new McpClientFactory();
    client = factory.createForStdio("node", [MOCK_SERVER_PATH], {});

    await expect(client.listTools()).rejects.toThrow("not connected");
  });

  it("throws when calling callTool before connect", async () => {
    const factory = new McpClientFactory();
    client = factory.createForStdio("node", [MOCK_SERVER_PATH], {});

    await expect(client.callTool("echo", {})).rejects.toThrow("not connected");
  });

  it("throws the MCP tool error text when a tool result has isError", async () => {
    const factory = new McpClientFactory();
    client = factory.createForStdio("node", [MOCK_SERVER_PATH], {});

    await client.connect();

    await expect(client.callTool("fail", {}))
      .rejects.toThrow("Mailbox authentication failed");
  });

  it("bounds the stderr tail when a server spams stderr during connect", async () => {
    const factory = new McpClientFactory();
    // Spam ~2 MB of stderr before handshake; connect must still complete.
    const previous = process.env.MCP_MOCK_SPAM_STDERR;
    process.env.MCP_MOCK_SPAM_STDERR = "150000";
    try {
      client = factory.createForStdio("node", [MOCK_SERVER_PATH], {});
      await client.connect();
      // Connect succeeded despite the flood — and listTools still works.
      const tools = await client.listTools();
      expect(tools[0]!.name).toBe("echo");
    } finally {
      if (previous === undefined) delete process.env.MCP_MOCK_SPAM_STDERR;
      else process.env.MCP_MOCK_SPAM_STDERR = previous;
    }
  });

  it("cleans up the connect race timers on timeout (no leaked handle)", async () => {
    const factory = new McpClientFactory();
    const previousTimeout = process.env.NUSASHELL_MCP_CONNECT_TIMEOUT;
    process.env.NUSASHELL_MCP_CONNECT_TIMEOUT = "80";
    const previousDelay = process.env.MCP_MOCK_DELAY_MS;
    process.env.MCP_MOCK_DELAY_MS = "1000";
    try {
      client = factory.createForStdio("node", [MOCK_SERVER_PATH], {});
      await expect(client.connect()).rejects.toThrow(/timed out/);
    } finally {
      if (previousTimeout === undefined) delete process.env.NUSASHELL_MCP_CONNECT_TIMEOUT;
      else process.env.NUSASHELL_MCP_CONNECT_TIMEOUT = previousTimeout;
      if (previousDelay === undefined) delete process.env.MCP_MOCK_DELAY_MS;
      else process.env.MCP_MOCK_DELAY_MS = previousDelay;
    }
  });
});

