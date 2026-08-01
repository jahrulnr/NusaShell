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
    expect(resolveStdioLaunch("node", { PLUGIN_ENV: "yes" }, {
      execPath: "/opt/NusaShell/NusaShell",
      electronVersion: "33.4.11",
    })).toEqual({
      command: "/opt/NusaShell/NusaShell",
      env: { PLUGIN_ENV: "yes", ELECTRON_RUN_AS_NODE: "1" },
    });
  });

  it("keeps the manifest command outside Electron", () => {
    expect(resolveStdioLaunch("node", { PLUGIN_ENV: "yes" }, {
      execPath: "/usr/bin/node",
    })).toEqual({ command: "node", env: { PLUGIN_ENV: "yes" } });
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
});
