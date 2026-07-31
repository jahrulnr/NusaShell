import { describe, expect, it, beforeEach, afterEach } from "vitest";
import { mkdtemp, rm, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { FilesystemMemoryStore } from "../src/index.js";
import { MEMORY_LIMIT } from "@nusashell/application";

let tempRoot: string;

beforeEach(async () => {
  tempRoot = await mkdtemp(join(tmpdir(), "nusa-mem-"));
});

afterEach(async () => {
  await rm(tempRoot, { recursive: true, force: true });
});

describe("FilesystemMemoryStore", () => {
  it("returns empty snapshot when no files exist", async () => {
    const store = new FilesystemMemoryStore(tempRoot);
    const snap = await store.loadSnapshot();
    expect(snap.memory).toEqual([]);
    expect(snap.user).toEqual([]);
    expect(snap.usage.memory.chars).toBe(0);
    expect(snap.usage.user.chars).toBe(0);
  });

  it("adds an entry and persists to MEMORY.md", async () => {
    const store = new FilesystemMemoryStore(tempRoot);
    const result = await store.add("memory", "first note");
    expect(result.ok).toBe(true);
    expect(result.data.entries).toEqual([{ text: "first note" }]);
    const raw = await readFile(join(tempRoot, "MEMORY.md"), "utf8");
    expect(raw).toBe("first note");
  });

  it("adds entries to USER.md", async () => {
    const store = new FilesystemMemoryStore(tempRoot);
    await store.add("user", "likes concise answers");
    const raw = await readFile(join(tempRoot, "USER.md"), "utf8");
    expect(raw).toBe("likes concise answers");
  });

  it("loads persisted entries on next snapshot", async () => {
    const store = new FilesystemMemoryStore(tempRoot);
    await store.add("memory", "alpha");
    await store.add("memory", "beta");
    const snap = await store.loadSnapshot();
    expect(snap.memory).toEqual([{ text: "alpha" }, { text: "beta" }]);
  });

  it("replaces an entry by unique substring", async () => {
    const store = new FilesystemMemoryStore(tempRoot);
    await store.add("memory", "old content here");
    const result = await store.replace("memory", "old content", "new content");
    expect(result.data.entries).toEqual([{ text: "new content" }]);
    const raw = await readFile(join(tempRoot, "MEMORY.md"), "utf8");
    expect(raw).toBe("new content");
  });

  it("removes an entry by unique substring", async () => {
    const store = new FilesystemMemoryStore(tempRoot);
    await store.add("memory", "keep this");
    await store.add("memory", "remove this");
    const result = await store.remove("memory", "remove");
    expect(result.data.entries).toEqual([{ text: "keep this" }]);
  });

  it("throws on ambiguous match in replace", async () => {
    const store = new FilesystemMemoryStore(tempRoot);
    await store.add("memory", "dup entry");
    await store.add("memory", "dup entry two");
    await expect(store.replace("memory", "dup", "new")).rejects.toThrow(/multiple entries/);
  });

  it("throws on no match in remove", async () => {
    const store = new FilesystemMemoryStore(tempRoot);
    await store.add("memory", "hello");
    await expect(store.remove("memory", "xyz")).rejects.toThrow(/did not match/);
  });

  it("throws on capacity overflow", async () => {
    const store = new FilesystemMemoryStore(tempRoot);
    const long = "x".repeat(MEMORY_LIMIT + 1);
    await expect(store.add("memory", long)).rejects.toThrow(/capacity exceeded/);
  });

  it("ignores empty content in add", async () => {
    const store = new FilesystemMemoryStore(tempRoot);
    await store.add("memory", "first");
    const result = await store.add("memory", "   ");
    expect(result.data.entries).toEqual([{ text: "first" }]);
  });

  it("creates root directory on first write", async () => {
    const nestedRoot = join(tempRoot, "nested", "deep");
    const store = new FilesystemMemoryStore(nestedRoot);
    await store.add("memory", "test");
    const raw = await readFile(join(nestedRoot, "MEMORY.md"), "utf8");
    expect(raw).toBe("test");
  });

  it("writes atomically (no .tmp file left behind)", async () => {
    const store = new FilesystemMemoryStore(tempRoot);
    await store.add("memory", "atomic test");
    const { readdir } = await import("node:fs/promises");
    const files = await readdir(tempRoot);
    expect(files).not.toContain("MEMORY.md.tmp");
  });
});
