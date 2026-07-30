import { describe, it, expect, beforeEach, afterEach } from "vitest";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import { loadRootFromEnvironment, resolvePath } from "../mcp/config.js";

let tmpDir;

beforeEach(async () => {
  tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), "files-config-test-"));
});

afterEach(async () => {
  await fs.rm(tmpDir, { recursive: true, force: true });
});

describe("loadRootFromEnvironment", () => {
  it("uses NUSASHELL_FILES_ROOT when set", async () => {
    const root = await loadRootFromEnvironment({ NUSASHELL_FILES_ROOT: tmpDir });
    expect(root).toBe(path.resolve(tmpDir));
  });

  it("falls back to home directory when not set", async () => {
    const root = await loadRootFromEnvironment({});
    expect(root).toBe(os.homedir());
  });

  it("throws when root does not exist", async () => {
    await expect(
      loadRootFromEnvironment({ NUSASHELL_FILES_ROOT: "/nonexistent/path/xyz" }),
    ).rejects.toThrow(/does not exist/);
  });

  it("throws when root is not a directory", async () => {
    const filePath = path.join(tmpDir, "file.txt");
    await fs.writeFile(filePath, "x");
    await expect(
      loadRootFromEnvironment({ NUSASHELL_FILES_ROOT: filePath }),
    ).rejects.toThrow(/not a directory/);
  });
});

describe("resolvePath", () => {
  it("returns root for empty input", () => {
    expect(resolvePath(tmpDir, "")).toBe(tmpDir);
    expect(resolvePath(tmpDir, "/")).toBe(tmpDir);
  });

  it("resolves relative paths against root", () => {
    expect(resolvePath(tmpDir, "sub/file.txt")).toBe(path.resolve(tmpDir, "sub/file.txt"));
  });

  it("resolves absolute paths as-is", () => {
    expect(resolvePath(tmpDir, "/absolute/path")).toBe("/absolute/path");
  });
});
