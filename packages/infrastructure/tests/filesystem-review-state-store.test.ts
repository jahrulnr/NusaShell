import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mkdtemp, rm, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { FilesystemReviewStateStore } from "../src/agent/filesystem-review-state-store.js";

describe("FilesystemReviewStateStore", () => {
  let dir: string;

  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), "review-state-"));
  });

  afterEach(async () => {
    await rm(dir, { recursive: true, force: true });
  });

  it("returns default state when file does not exist", async () => {
    const store = new FilesystemReviewStateStore(dir);
    const state = await store.load();
    expect(state.turnsSinceMemory).toBe(0);
    expect(state.toolRoundsSinceSkill).toBe(0);
    expect(state.lastReviewAt).toBeUndefined();
  });

  it("saves and loads state", async () => {
    const store = new FilesystemReviewStateStore(dir);
    await store.save({
      turnsSinceMemory: 5,
      toolRoundsSinceSkill: 12,
      lastReviewAt: "2025-01-01T00:00:00Z",
    });
    const state = await store.load();
    expect(state.turnsSinceMemory).toBe(5);
    expect(state.toolRoundsSinceSkill).toBe(12);
    expect(state.lastReviewAt).toBe("2025-01-01T00:00:00Z");
  });

  it("persists to .review-state.json", async () => {
    const store = new FilesystemReviewStateStore(dir);
    await store.save({ turnsSinceMemory: 3, toolRoundsSinceSkill: 7 });
    const raw = await readFile(join(dir, ".review-state.json"), "utf8");
    const parsed = JSON.parse(raw);
    expect(parsed.turnsSinceMemory).toBe(3);
    expect(parsed.toolRoundsSinceSkill).toBe(7);
  });

  it("handles missing fields gracefully", async () => {
    const store = new FilesystemReviewStateStore(dir);
    await store.save({ turnsSinceMemory: 1, toolRoundsSinceSkill: 0 });
    const state = await store.load();
    expect(state.turnsSinceMemory).toBe(1);
    expect(state.toolRoundsSinceSkill).toBe(0);
  });
});
