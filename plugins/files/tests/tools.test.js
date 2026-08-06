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

  it("list returns items array", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "x");
    const result = await callFilesTool(service, "list", { path: "" });
    expect(result.path).toBe("");
    expect(result.items).toHaveLength(1);
    expect(result.items[0].name).toBe("test.txt");
  });

  it("list defaults to root when no path", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "x");
    const result = await callFilesTool(service, "list", {});
    expect(result.items).toHaveLength(1);
  });

  it("tree returns tree structure", async () => {
    await fs.mkdir(path.join(tmpDir, "dir"));
    const result = await callFilesTool(service, "tree", { path: "" });
    expect(result.tree).toHaveLength(1);
    expect(result.tree[0].name).toBe("dir");
  });

  it("read returns content", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "hello");
    const result = await callFilesTool(service, "read", { path: "test.txt" });
    expect(result.content).toBe("hello");
  });

  it("write creates a file", async () => {
    const result = await callFilesTool(service, "write", {
      path: "new.txt",
      content: "data",
    });
    expect(result.written).toBe(true);
    expect(await fs.readFile(path.join(tmpDir, "new.txt"), "utf8")).toBe("data");
  });

  it("mkdir creates an empty directory", async () => {
    const result = await callFilesTool(service, "mkdir", { path: "empty/nested" });
    expect(result.created).toBe(true);
    expect((await fs.stat(path.join(tmpDir, "empty", "nested"))).isDirectory()).toBe(true);
  });

  it("move moves a file", async () => {
    await fs.writeFile(path.join(tmpDir, "src.txt"), "x");
    const result = await callFilesTool(service, "move", {
      source: "src.txt",
      destination: "dst.txt",
    });
    expect(result.moved).toBe(true);
  });

  it("delete deletes a file", async () => {
    await fs.writeFile(path.join(tmpDir, "file.txt"), "x");
    const result = await callFilesTool(service, "delete", {
      path: "file.txt",
      recursive: false,
    });
    expect(result.deleted).toBe(true);
  });

  it("search finds matching files", async () => {
    await fs.writeFile(path.join(tmpDir, "a.txt"), "x");
    await fs.writeFile(path.join(tmpDir, "b.md"), "x");
    const result = await callFilesTool(service, "search", {
      path: "",
      pattern: "*.txt",
    });
    expect(result.results).toHaveLength(1);
    expect(result.results[0].name).toBe("a.txt");
  });

  it("info returns metadata", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "hello");
    const result = await callFilesTool(service, "info", { path: "test.txt" });
    expect(result.name).toBe("test.txt");
    expect(result.size).toBe(5);
  });

  it("clamps grep after/before/maxResults past caps instead of rejecting", async () => {
    await fs.writeFile(
      path.join(tmpDir, "a.ts"),
      "l0\nl1\nl2\nMATCH\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11\nl12\n",
    );
    const result = await callFilesTool(service, "grep", {
      path: "a.ts",
      pattern: "MATCH",
      after: 12,
      before: -3,
      maxResults: 5000,
    });
    expect(result.results).toHaveLength(1);
    expect(result.results[0].line).toBe(4);
    // before clamped to 0 → no before lines; after clamped to 10
    expect(result.results[0].before ?? []).toEqual([]);
    expect(result.results[0].after).toHaveLength(10);
    // maxResults recovers (clamped); service also floors by internal search cap
    expect(result.meta.cap).toBeGreaterThanOrEqual(1);
    expect(result.meta.count).toBe(1);
  });

  it("rejects invalid input (missing required path)", async () => {
    await expect(callFilesTool(service, "read", {})).rejects.toThrow();
  });

  it("rejects invalid input (extra fields)", async () => {
    await expect(
      callFilesTool(service, "list", { path: "", extra: true }),
    ).rejects.toThrow();
  });
});

describe("workspace context tools", () => {
  it("context_map returns map, stack, ranks, and stats", async () => {
    await fs.writeFile(
      path.join(tmpDir, "package.json"),
      JSON.stringify({ name: "demo", version: "1.0.0" }),
    );
    await fs.writeFile(path.join(tmpDir, "index.ts"), "export function main() { return 1; }\n");
    const result = await callFilesTool(service, "context_map", {});
    expect(typeof result.map).toBe("string");
    expect(result.map).toContain("index.ts");
    expect(result.stack.category).toBe("coding");
    expect(Array.isArray(result.ranks)).toBe(true);
    expect(result.stats.tokensUsed).toBeGreaterThan(0);
  });

  it("context_map clamps over-limit budget and rejects extra fields", async () => {
    await fs.writeFile(path.join(tmpDir, "README.md"), "# docs\n");
    const result = await callFilesTool(service, "context_map", { budget: 99999 });
    expect(typeof result.map).toBe("string");
    expect(result.stats.tokensUsed).toBeLessThanOrEqual(8192);
    await expect(callFilesTool(service, "context_map", { nope: 1 })).rejects.toThrow();
  });

  it("detect_stack returns the workspace classification", async () => {
    await fs.writeFile(path.join(tmpDir, "README.md"), "# docs\n");
    const result = await callFilesTool(service, "detect_stack", {});
    expect(result.category).toBe("documentation");
    expect(result.isMonorepo).toBe(false);
  });

  it("list_symbols returns definitions for one file", async () => {
    await fs.writeFile(path.join(tmpDir, "a.py"), "def start():\n    pass\n");
    const result = await callFilesTool(service, "list_symbols", { path: "a.py" });
    expect(result.symbols.map((s) => s.name)).toContain("start");
  });

  it("list_symbols requires either path or query", async () => {
    await expect(callFilesTool(service, "list_symbols", {})).rejects.toThrow();
  });

  it("context tools are annotated read-only and non-destructive", async () => {
    for (const tool of FILES_TOOLS) {
      if (["context_map", "detect_stack", "list_symbols"].includes(tool.name)) {
        expect(tool.annotations.readOnlyHint).toBe(true);
        expect(tool.annotations.destructiveHint).toBe(false);
      }
    }
  });
});
