import { describe, it, expect, beforeEach, afterEach } from "vitest";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import { FileService, detectFileType, formatFileSize } from "../mcp/fs-service.js";

let tmpDir;
let service;

beforeEach(async () => {
  tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), "files-test-"));
  service = new FileService(tmpDir);
});

afterEach(async () => {
  await fs.rm(tmpDir, { recursive: true, force: true });
});

describe("detectFileType", () => {
  it("detects text files", () => {
    expect(detectFileType("readme.md")).toBe("text");
    expect(detectFileType("app.js")).toBe("text");
    expect(detectFileType("data.json")).toBe("text");
  });

  it("detects image files", () => {
    expect(detectFileType("photo.png")).toBe("image");
    expect(detectFileType("photo.jpg")).toBe("image");
  });

  it("detects binary for unknown extensions", () => {
    expect(detectFileType("data.dat")).toBe("binary");
  });
});

describe("formatFileSize", () => {
  it("formats bytes", () => {
    expect(formatFileSize(0)).toBe("0 B");
    expect(formatFileSize(512)).toBe("512 B");
    expect(formatFileSize(1024)).toBe("1.0 KB");
    expect(formatFileSize(1024 * 1024)).toBe("1.0 MB");
  });
});

describe("FileService.listDir", () => {
  it("lists files and directories sorted dirs-first", async () => {
    await fs.writeFile(path.join(tmpDir, "b.txt"), "b");
    await fs.mkdir(path.join(tmpDir, "afolder"));
    await fs.writeFile(path.join(tmpDir, "a.txt"), "a");

    const items = await service.listDir("/");
    expect(items).toHaveLength(3);
    expect(items[0].name).toBe("afolder");
    expect(items[0].isDir).toBe(true);
    expect(items[1].name).toBe("a.txt");
    expect(items[2].name).toBe("b.txt");
  });

  it("includes file metadata", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "hello");
    const items = await service.listDir("/");
    expect(items[0].size).toBe(5);
    expect(items[0].type).toBe("text");
    expect(items[0].modified).toBeTruthy();
  });
});

describe("FileService.tree", () => {
  it("builds a tree with default depth", async () => {
    await fs.mkdir(path.join(tmpDir, "dir1", "dir2"), { recursive: true });
    await fs.writeFile(path.join(tmpDir, "dir1", "file.txt"), "x");
    await fs.writeFile(path.join(tmpDir, "dir1", "dir2", "deep.txt"), "y");

    const tree = await service.tree("/");
    expect(tree).toHaveLength(1);
    expect(tree[0].name).toBe("dir1");
    expect(tree[0].children).toHaveLength(2);
    const dir2 = tree[0].children.find((c) => c.name === "dir2");
    expect(dir2).toBeDefined();
    // depth 3: dir1 > dir2 > deep.txt should be visible
    expect(dir2.children).toHaveLength(1);
  });

  it("respects depth limit", async () => {
    await fs.mkdir(path.join(tmpDir, "a", "b", "c"), { recursive: true });
    const tree = await service.tree("/", 1);
    expect(tree).toHaveLength(1);
    expect(tree[0].name).toBe("a");
    expect(tree[0].children).toBeUndefined();
  });
});

describe("FileService.readFile", () => {
  it("reads full file content", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "line1\nline2\nline3");
    const result = await service.readFile("test.txt");
    expect(result.content).toBe("line1\nline2\nline3");
    expect(result.totalLines).toBe(3);
    expect(result.truncated).toBe(false);
  });

  it("reads head lines", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "line1\nline2\nline3");
    const result = await service.readFile("test.txt", 2);
    expect(result.content).toBe("line1\nline2");
    expect(result.truncated).toBe(true);
  });

  it("reads tail lines", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "line1\nline2\nline3");
    const result = await service.readFile("test.txt", undefined, 1);
    expect(result.content).toBe("line3");
    expect(result.truncated).toBe(true);
  });
});

describe("FileService.writeFile", () => {
  it("creates a new file", async () => {
    const result = await service.writeFile("new.txt", "content");
    expect(result.written).toBe(true);
    const content = await fs.readFile(path.join(tmpDir, "new.txt"), "utf8");
    expect(content).toBe("content");
  });

  it("creates parent directories", async () => {
    await service.writeFile("sub/dir/file.txt", "nested");
    const content = await fs.readFile(path.join(tmpDir, "sub", "dir", "file.txt"), "utf8");
    expect(content).toBe("nested");
  });

  it("overwrites existing file", async () => {
    await service.writeFile("file.txt", "old");
    await service.writeFile("file.txt", "new");
    const content = await fs.readFile(path.join(tmpDir, "file.txt"), "utf8");
    expect(content).toBe("new");
  });
});

describe("FileService.makeDir", () => {
  it("creates an empty nested directory", async () => {
    const result = await service.makeDir("empty/nested");
    expect(result.created).toBe(true);
    expect((await fs.stat(path.join(tmpDir, "empty", "nested"))).isDirectory()).toBe(true);
  });
});

describe("FileService.moveFile", () => {
  it("moves a file", async () => {
    await fs.writeFile(path.join(tmpDir, "src.txt"), "data");
    const result = await service.moveFile("src.txt", "dst.txt");
    expect(result.moved).toBe(true);
    await expect(fs.stat(path.join(tmpDir, "src.txt"))).rejects.toThrow();
    const content = await fs.readFile(path.join(tmpDir, "dst.txt"), "utf8");
    expect(content).toBe("data");
  });

  it("moves into nested destination", async () => {
    await fs.writeFile(path.join(tmpDir, "file.txt"), "data");
    await service.moveFile("file.txt", "nested/deep/file.txt");
    const content = await fs.readFile(path.join(tmpDir, "nested", "deep", "file.txt"), "utf8");
    expect(content).toBe("data");
  });
});

describe("FileService.deleteFile", () => {
  it("deletes a file", async () => {
    await fs.writeFile(path.join(tmpDir, "file.txt"), "x");
    const result = await service.deleteFile("file.txt", false);
    expect(result.deleted).toBe(true);
    await expect(fs.stat(path.join(tmpDir, "file.txt"))).rejects.toThrow();
  });

  it("refuses non-empty directory without recursive", async () => {
    await fs.mkdir(path.join(tmpDir, "dir"));
    await fs.writeFile(path.join(tmpDir, "dir", "file.txt"), "x");
    await expect(service.deleteFile("dir", false)).rejects.toThrow(/not empty/i);
  });

  it("deletes non-empty directory with recursive", async () => {
    await fs.mkdir(path.join(tmpDir, "dir"));
    await fs.writeFile(path.join(tmpDir, "dir", "file.txt"), "x");
    const result = await service.deleteFile("dir", true);
    expect(result.deleted).toBe(true);
  });

  it("refuses to delete relative paths that escape root via traversal", async () => {
    await expect(service.deleteFile("../../etc", true)).rejects.toThrow(/escapes files root/);
  });

  it("allows absolute paths (agent is a trusted actor)", async () => {
    const absFile = path.join(tmpDir, "abs-file.txt");
    await fs.writeFile(absFile, "x");
    const result = await service.deleteFile(absFile, false);
    expect(result.deleted).toBe(true);
  });
});

describe("FileService.searchFiles", () => {
  it("finds files matching a glob pattern", async () => {
    await fs.writeFile(path.join(tmpDir, "a.txt"), "x");
    await fs.writeFile(path.join(tmpDir, "b.txt"), "x");
    await fs.writeFile(path.join(tmpDir, "c.md"), "x");
    await fs.mkdir(path.join(tmpDir, "sub"));
    await fs.writeFile(path.join(tmpDir, "sub", "d.txt"), "x");

    const results = await service.searchFiles("/", "*.txt");
    expect(results).toHaveLength(3);
    expect(results.map((r) => r.name).sort()).toEqual(["a.txt", "b.txt", "d.txt"]);
  });

  it("supports ? wildcard", async () => {
    await fs.writeFile(path.join(tmpDir, "ab.txt"), "x");
    await fs.writeFile(path.join(tmpDir, "cd.txt"), "x");
    const results = await service.searchFiles("/", "?b.txt");
    expect(results).toHaveLength(1);
    expect(results[0].name).toBe("ab.txt");
  });
});

describe("FileService.fileInfo", () => {
  it("returns detailed metadata for a file", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "hello");
    const info = await service.fileInfo("test.txt");
    expect(info.name).toBe("test.txt");
    expect(info.isFile).toBe(true);
    expect(info.isDir).toBe(false);
    expect(info.size).toBe(5);
    expect(info.type).toBe("text");
    expect(info.permissions).toBeTruthy();
  });

  it("returns metadata for a directory", async () => {
    await fs.mkdir(path.join(tmpDir, "mydir"));
    const info = await service.fileInfo("mydir");
    expect(info.isDir).toBe(true);
    expect(info.type).toBe("dir");
  });
});

describe("FileService error hints", () => {
  it("includes root path hint on ENOENT for listDir", async () => {
    await expect(service.listDir("nonexistent")).rejects.toThrow(/Files plugin root/);
  });

  it("includes root path hint on ENOENT for readFile", async () => {
    await expect(service.readFile("missing.txt")).rejects.toThrow(/Files plugin root/);
  });

  it("includes root path hint on ENOENT for fileInfo", async () => {
    await expect(service.fileInfo("missing.txt")).rejects.toThrow(/Files plugin root/);
  });

  it("includes root path hint on ENOENT for deleteFile", async () => {
    await expect(service.deleteFile("missing.txt", false)).rejects.toThrow(/Files plugin root/);
  });

  it("includes root path hint on ENOENT for tree", async () => {
    await expect(service.tree("nonexistent")).rejects.toThrow(/Files plugin root/);
  });

  it("includes root path hint on ENOENT for searchFiles", async () => {
    await expect(service.searchFiles("nonexistent", "*.txt")).rejects.toThrow(/Files plugin root/);
  });
});

describe("FileService.grepFiles", () => {
  it("finds matching lines in text files", async () => {
    await fs.writeFile(path.join(tmpDir, "a.js"), "function foo() {}\nconst x = 1;\nfunction bar() {}");
    await fs.writeFile(path.join(tmpDir, "b.js"), "const y = 2;\nfunction baz() {}");
    await fs.writeFile(path.join(tmpDir, "c.md"), "# Hello\nfunction notMatched() {}");

    const results = await service.grepFiles("/", "function\\s+\\w+");
    expect(results).toHaveLength(4);
    expect(results.every((r) => r.line > 0)).toBe(true);
    expect(results.every((r) => r.content.includes("function"))).toBe(true);
  });

  it("filters by glob pattern", async () => {
    await fs.writeFile(path.join(tmpDir, "a.js"), "function foo() {}");
    await fs.writeFile(path.join(tmpDir, "b.ts"), "function bar() {}");
    await fs.writeFile(path.join(tmpDir, "c.md"), "function baz() {}");

    const results = await service.grepFiles("/", "function", "*.js");
    expect(results).toHaveLength(1);
    expect(results[0].path).toBe("a.js");
  });

  it("skips non-text files", async () => {
    await fs.writeFile(path.join(tmpDir, "data.bin"), Buffer.from([0x00, 0x01, 0x02]));
    await fs.writeFile(path.join(tmpDir, "a.txt"), "hello world");

    const results = await service.grepFiles("/", "hello");
    expect(results).toHaveLength(1);
    expect(results[0].path).toBe("a.txt");
  });

  it("searches recursively", async () => {
    await fs.mkdir(path.join(tmpDir, "sub"));
    await fs.writeFile(path.join(tmpDir, "sub", "deep.js"), "TODO: fix this");

    const results = await service.grepFiles("/", "TODO");
    expect(results).toHaveLength(1);
    expect(results[0].path).toBe(path.join("sub", "deep.js"));
    expect(results[0].line).toBe(1);
  });

  it("includes root path hint on ENOENT", async () => {
    await expect(service.grepFiles("nonexistent", "pattern")).rejects.toThrow(/Files plugin root/);
  });
});

describe("FileService.patchFile", () => {
  it("replaces first occurrence of old_string", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "hello world\nfoo bar");
    const result = await service.patchFile("test.txt", "foo bar", "baz qux");
    expect(result.patched).toBe(true);
    const content = await fs.readFile(path.join(tmpDir, "test.txt"), "utf8");
    expect(content).toBe("hello world\nbaz qux");
  });

  it("only replaces first occurrence", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "aaa\naaa\naaa");
    await service.patchFile("test.txt", "aaa", "bbb");
    const content = await fs.readFile(path.join(tmpDir, "test.txt"), "utf8");
    expect(content).toBe("bbb\naaa\naaa");
  });

  it("throws if old_string not found", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "hello world");
    await expect(service.patchFile("test.txt", "missing", "replacement")).rejects.toThrow(/old_string not found/);
  });

  it("throws on ENOENT with root hint", async () => {
    await expect(service.patchFile("missing.txt", "a", "b")).rejects.toThrow(/Files plugin root/);
  });
});

describe("FileService.appendFile", () => {
  it("appends to an existing file", async () => {
    await fs.writeFile(path.join(tmpDir, "log.txt"), "line1\n");
    const result = await service.appendFile("log.txt", "line2\n");
    expect(result.appended).toBe(true);
    const content = await fs.readFile(path.join(tmpDir, "log.txt"), "utf8");
    expect(content).toBe("line1\nline2\n");
  });

  it("creates a new file if it does not exist", async () => {
    const result = await service.appendFile("new.txt", "content");
    expect(result.appended).toBe(true);
    const content = await fs.readFile(path.join(tmpDir, "new.txt"), "utf8");
    expect(content).toBe("content");
  });

  it("creates parent directories", async () => {
    await service.appendFile("sub/dir/file.txt", "nested");
    const content = await fs.readFile(path.join(tmpDir, "sub", "dir", "file.txt"), "utf8");
    expect(content).toBe("nested");
  });
});

describe("FileService.copyFile", () => {
  it("copies a file", async () => {
    await fs.writeFile(path.join(tmpDir, "original.txt"), "hello world");
    const result = await service.copyFile("original.txt", "copy.txt");
    expect(result.copied).toBe(true);
    expect(result.from).toBe("original.txt");
    expect(result.to).toBe("copy.txt");
    const content = await fs.readFile(path.join(tmpDir, "copy.txt"), "utf8");
    expect(content).toBe("hello world");
    const original = await fs.readFile(path.join(tmpDir, "original.txt"), "utf8");
    expect(original).toBe("hello world");
  });

  it("copies a directory recursively", async () => {
    await fs.mkdir(path.join(tmpDir, "srcdir"));
    await fs.writeFile(path.join(tmpDir, "srcdir", "a.txt"), "aaa");
    await fs.writeFile(path.join(tmpDir, "srcdir", "b.txt"), "bbb");

    const result = await service.copyFile("srcdir", "dstdir");
    expect(result.copied).toBe(true);
    const aContent = await fs.readFile(path.join(tmpDir, "dstdir", "a.txt"), "utf8");
    const bContent = await fs.readFile(path.join(tmpDir, "dstdir", "b.txt"), "utf8");
    expect(aContent).toBe("aaa");
    expect(bContent).toBe("bbb");
  });

  it("creates parent directories for destination", async () => {
    await fs.writeFile(path.join(tmpDir, "file.txt"), "content");
    await service.copyFile("file.txt", "sub/deep/copy.txt");
    const content = await fs.readFile(path.join(tmpDir, "sub", "deep", "copy.txt"), "utf8");
    expect(content).toBe("content");
  });

  it("throws on ENOENT with root hint", async () => {
    await expect(service.copyFile("missing.txt", "copy.txt")).rejects.toThrow(/Files plugin root/);
  });

  it("respects path sandboxing", async () => {
    await fs.writeFile(path.join(tmpDir, "file.txt"), "content");
    await expect(service.copyFile("file.txt", "../../../etc/copy")).rejects.toThrow(
      "Path escapes files root",
    );
  });
});

describe("FileService.setRoot", () => {
  it("updates the in-process root and subsequent operations use it", async () => {
    const newRoot = await fs.mkdtemp(path.join(os.tmpdir(), "files-setroot-"));
    try {
      await fs.writeFile(path.join(newRoot, "hello.txt"), "hi");
      await service.setRoot(newRoot);
      expect(service.root).toBe(path.resolve(newRoot));
      const result = await service.readFile("hello.txt");
      expect(result.content).toBe("hi");
    } finally {
      await fs.rm(newRoot, { recursive: true, force: true });
    }
  });

  it("rejects a non-existent root", async () => {
    await expect(service.setRoot("/nonexistent/path/xyz")).rejects.toThrow(/does not exist/);
  });

  it("rejects a root that is not a directory", async () => {
    const filePath = path.join(tmpDir, "not-a-dir.txt");
    await fs.writeFile(filePath, "x");
    await expect(service.setRoot(filePath)).rejects.toThrow(/not a directory/);
  });
});
