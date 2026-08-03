import { describe, expect, it, afterEach, beforeEach } from "vitest";
import { McpClientFactory } from "../src/mcp/mcp-client.factory.js";
import type { McpClientPort } from "@nusashell/application";
import { resolve, join } from "node:path";
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
 * stdio MCP and asserts that relative path-escape attempts are rejected at
 * runtime — not just in source. A stale bundle that predates the `resolvePath`
 * guard would let `../../etc/passwd` read outside the sandbox. Absolute paths
 * are allowed (the agent is a trusted actor); only `../` traversal from
 * relative paths is rejected.
 *
 * See plan: plugin_sandbox_readiness_b0476ef9 — P0.
 */
describe("Files bundle sandbox (runtime escape regression)", () => {
  let client: McpClientPort | null = null;
  let tmpRoot: string;
  let outsideFile: string;

  beforeEach(async () => {
    tmpRoot = await fs.mkdtemp(join(os.tmpdir(), "files-bundle-sandbox-"));
    // Seed a marker file inside the root so a positive read proves containment.
    await fs.writeFile(resolve(tmpRoot, "inside.txt"), "sandbox-marker");
    // Create a file OUTSIDE the root to prove absolute paths are accepted.
    // Using a real temp file makes the test cross-platform: on Windows,
    // "/etc/hostname" is not absolute and would be rejected as root-escape.
    const outsideDir = await fs.mkdtemp(join(os.tmpdir(), "files-bundle-outside-"));
    outsideFile = resolve(outsideDir, "outside.txt");
    await fs.writeFile(outsideFile, "outside-marker");
  });

  afterEach(async () => {
    if (client) {
      await client.close();
      client = null;
    }
    await fs.rm(tmpRoot, { recursive: true, force: true });
    await fs.rm(resolve(outsideFile, ".."), { recursive: true, force: true });
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

  it("allows absolute paths (agent is a trusted actor)", async () => {
    const factory = new McpClientFactory();
    client = factory.createForStdio("node", [BUNDLE_PATH], {
      NUSASHELL_FILES_ROOT: tmpRoot,
    });
    await client.connect();

    // Read a file outside the root via its absolute path. The read should
    // succeed (or fail with ENOENT) — but must NOT be rejected with
    // "escapes files root", which would mean the path was treated as relative.
    try {
      const result = await client.callTool("files_read", { path: outsideFile });
      expect(JSON.stringify(result)).toContain("outside-marker");
    } catch (error) {
      const msg = error instanceof Error ? error.message : String(error);
      expect(msg).not.toMatch(/escapes files root/);
    }
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
