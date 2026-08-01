import { describe, expect, it, afterEach, beforeEach } from "vitest";
import { McpClientFactory } from "../src/mcp/mcp-client.factory.js";
import type { McpClientPort } from "@nusashell/application";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import fs from "node:fs/promises";
import os from "node:os";

const BUNDLE_PATH = resolve(
  fileURLToPath(import.meta.url),
  "..",
  "..",
  "..",
  "..",
  "plugins",
  "files",
  "mcp",
  "server.cjs",
);

/**
 * Runtime regression guard for finding 1 (Files path escape via stale bundle).
 *
 * Spawns the *shipped* esbuild bundle (`plugins/files/mcp/server.cjs`) over
 * stdio MCP and asserts that path-escape attempts are rejected at runtime —
 * not just in source. A stale bundle that predates the `resolvePath` guard
 * would let `../../etc/passwd` and absolute paths read outside the sandbox.
 *
 * See plan: plugin_sandbox_readiness_b0476ef9 — P0.
 */
describe("Files bundle sandbox (runtime escape regression)", () => {
  let client: McpClientPort | null = null;
  let tmpRoot: string;

  beforeEach(async () => {
    tmpRoot = await fs.mkdtemp(os.tmpdir() + "/files-bundle-sandbox-");
    // Seed a marker file inside the root so a positive read proves containment.
    await fs.writeFile(resolve(tmpRoot, "inside.txt"), "sandbox-marker");
  });

  afterEach(async () => {
    if (client) {
      await client.close();
      client = null;
    }
    await fs.rm(tmpRoot, { recursive: true, force: true });
  });

  it("rejects ../../ traversal outside the root", async () => {
    const factory = new McpClientFactory();
    client = factory.createForStdio("node", [BUNDLE_PATH], {
      NUSASHELL_FILES_ROOT: tmpRoot,
    });
    await client.connect();

    await expect(
      client.callTool("files_read", { path: "../../etc/passwd" }),
    ).rejects.toThrow(/escapes files root/);
  });

  it("rejects absolute paths outside the root", async () => {
    const factory = new McpClientFactory();
    client = factory.createForStdio("node", [BUNDLE_PATH], {
      NUSASHELL_FILES_ROOT: tmpRoot,
    });
    await client.connect();

    await expect(
      client.callTool("files_read", { path: "/etc/passwd" }),
    ).rejects.toThrow(/escapes files root/);
  });

  it("allows reading a file inside the root (positive control)", async () => {
    const factory = new McpClientFactory();
    client = factory.createForStdio("node", [BUNDLE_PATH], {
      NUSASHELL_FILES_ROOT: tmpRoot,
    });
    await client.connect();

    const result = await client.callTool("files_read", { path: "inside.txt" });
    expect(JSON.stringify(result)).toContain("sandbox-marker");
  });
});
