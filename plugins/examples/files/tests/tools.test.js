import { describe, it, expect, beforeEach, afterEach } from "vitest";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import { FileService } from "../mcp/fs-service.js";
import { callFilesTool, FILES_TOOLS } from "../mcp/tools.js";
import { FILES_TOOL_NAMES } from "../mcp/tool-catalog.js";

let tmpDir;
let service;

beforeEach(async () => {
  tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), "files-tools-test-"));
  service = new FileService(tmpDir);
});

afterEach(async () => {
  await fs.rm(tmpDir, { recursive: true, force: true });
});

describe("FILES_TOOLS", () => {
  it("has exactly the canonical tool names", () => {
    expect(FILES_TOOLS.map((t) => t.name)).toEqual(FILES_TOOL_NAMES);
  });

  it("every tool has name, description, inputSchema, and annotations", () => {
    for (const tool of FILES_TOOLS) {
      expect(tool.name).toBeTruthy();
      expect(tool.description).toBeTruthy();
      expect(tool.inputSchema).toBeDefined();
      expect(tool.inputSchema.type).toBe("object");
      expect(tool.annotations).toBeDefined();
    }
  });
});

describe("callFilesTool", () => {
  it("rejects unknown tool name", async () => {
    await expect(callFilesTool(service, "unknown_tool", {})).rejects.toThrow();
  });

  it("files_list returns items array", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "x");
    const result = await callFilesTool(service, "files_list", { path: "/" });
    expect(result.path).toBe("/");
    expect(result.items).toHaveLength(1);
    expect(result.items[0].name).toBe("test.txt");
  });

  it("files_list defaults to root when no path", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "x");
    const result = await callFilesTool(service, "files_list", {});
    expect(result.items).toHaveLength(1);
  });

  it("files_tree returns tree structure", async () => {
    await fs.mkdir(path.join(tmpDir, "dir"));
    const result = await callFilesTool(service, "files_tree", { path: "/" });
    expect(result.tree).toHaveLength(1);
    expect(result.tree[0].name).toBe("dir");
  });

  it("files_read returns content", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "hello");
    const result = await callFilesTool(service, "files_read", { path: "test.txt" });
    expect(result.content).toBe("hello");
  });

  it("files_write creates a file", async () => {
    const result = await callFilesTool(service, "files_write", {
      path: "new.txt",
      content: "data",
    });
    expect(result.written).toBe(true);
    expect(await fs.readFile(path.join(tmpDir, "new.txt"), "utf8")).toBe("data");
  });

  it("files_mkdir creates an empty directory", async () => {
    const result = await callFilesTool(service, "files_mkdir", { path: "empty/nested" });
    expect(result.created).toBe(true);
    expect((await fs.stat(path.join(tmpDir, "empty", "nested"))).isDirectory()).toBe(true);
  });

  it("files_move moves a file", async () => {
    await fs.writeFile(path.join(tmpDir, "src.txt"), "x");
    const result = await callFilesTool(service, "files_move", {
      source: "src.txt",
      destination: "dst.txt",
    });
    expect(result.moved).toBe(true);
  });

  it("files_delete deletes a file", async () => {
    await fs.writeFile(path.join(tmpDir, "file.txt"), "x");
    const result = await callFilesTool(service, "files_delete", {
      path: "file.txt",
      recursive: false,
    });
    expect(result.deleted).toBe(true);
  });

  it("files_search finds matching files", async () => {
    await fs.writeFile(path.join(tmpDir, "a.txt"), "x");
    await fs.writeFile(path.join(tmpDir, "b.md"), "x");
    const result = await callFilesTool(service, "files_search", {
      path: "/",
      pattern: "*.txt",
    });
    expect(result.results).toHaveLength(1);
    expect(result.results[0].name).toBe("a.txt");
  });

  it("files_info returns metadata", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "hello");
    const result = await callFilesTool(service, "files_info", { path: "test.txt" });
    expect(result.name).toBe("test.txt");
    expect(result.size).toBe(5);
  });

  it("rejects invalid input (missing required path)", async () => {
    await expect(callFilesTool(service, "files_read", {})).rejects.toThrow();
  });

  it("rejects invalid input (extra fields)", async () => {
    await expect(
      callFilesTool(service, "files_list", { path: "/", extra: true }),
    ).rejects.toThrow();
  });
});
