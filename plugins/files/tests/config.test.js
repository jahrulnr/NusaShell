import { describe, it, expect, beforeEach, afterEach } from "vitest";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import { loadRootFromEnvironment, resolvePath, validateRoot } from "../mcp/config.js";

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

  it("falls back to NUSASHELL_WORKSPACE when NUSASHELL_FILES_ROOT is unset", async () => {
    const root = await loadRootFromEnvironment({ NUSASHELL_WORKSPACE: tmpDir });
    expect(root).toBe(path.resolve(tmpDir));
  });

  it("prefers NUSASHELL_FILES_ROOT over NUSASHELL_WORKSPACE", async () => {
    const root = await loadRootFromEnvironment({
      NUSASHELL_FILES_ROOT: tmpDir,
      NUSASHELL_WORKSPACE: "/nonexistent/should-not-be-used",
    });
    expect(root).toBe(path.resolve(tmpDir));
  });
});

describe("validateRoot", () => {
  it("resolves and validates an existing directory", async () => {
    const root = await validateRoot(tmpDir);
    expect(root).toBe(path.resolve(tmpDir));
  });

  it("throws when the path does not exist", async () => {
    await expect(validateRoot("/nonexistent/path/xyz")).rejects.toThrow(/does not exist/);
  });

  it("throws when the path is not a directory", async () => {
    const filePath = path.join(tmpDir, "file.txt");
    await fs.writeFile(filePath, "x");
    await expect(validateRoot(filePath)).rejects.toThrow(/not a directory/);
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

  it("rejects absolute paths outside root", () => {
    expect(() => resolvePath(tmpDir, "/absolute/path")).toThrow(/escapes files root/);
  });

  it("rejects relative paths that escape root via traversal", () => {
    expect(() => resolvePath(tmpDir, "../../etc/passwd")).toThrow(/escapes files root/);
    expect(() => resolvePath(tmpDir, "../../../")).toThrow(/escapes files root/);
  });

  it("allows nested paths inside root", () => {
    expect(resolvePath(tmpDir, "sub/dir/file.txt")).toBe(path.resolve(tmpDir, "sub/dir/file.txt"));
  });
});
