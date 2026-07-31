import { mkdtemp, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { FilesystemSkillUsage } from "../src/index.js";

describe("FilesystemSkillUsage", () => {
  it("bumps use count and sets lastUsedAt", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-usage-"));
    const usage = new FilesystemSkillUsage(root);
    await usage.record("my-skill", "use");
    const record = await usage.getRecord("my-skill");
    expect(record.useCount).toBe(1);
    expect(record.lastUsedAt).not.toBeNull();
  });

  it("bumps view count and sets lastViewedAt", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-usage-"));
    const usage = new FilesystemSkillUsage(root);
    await usage.record("my-skill", "view");
    const record = await usage.getRecord("my-skill");
    expect(record.viewCount).toBe(1);
    expect(record.lastViewedAt).not.toBeNull();
  });

  it("bumps patch count and sets lastPatchedAt", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-usage-"));
    const usage = new FilesystemSkillUsage(root);
    await usage.record("my-skill", "patch");
    const record = await usage.getRecord("my-skill");
    expect(record.patchCount).toBe(1);
    expect(record.lastPatchedAt).not.toBeNull();
  });

  it("persists atomically across instances", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-usage-"));
    const usage1 = new FilesystemSkillUsage(root);
    await usage1.record("persisted", "use");
    const usage2 = new FilesystemSkillUsage(root);
    const record = await usage2.getRecord("persisted");
    expect(record.useCount).toBe(1);
  });

  it("setState updates state and archivedAt", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-usage-"));
    const usage = new FilesystemSkillUsage(root);
    await usage.setState("my-skill", "archived");
    const record = await usage.getRecord("my-skill");
    expect(record.state).toBe("archived");
    expect(record.archivedAt).not.toBeNull();
  });

  it("setPinned toggles pinned flag", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-usage-"));
    const usage = new FilesystemSkillUsage(root);
    await usage.setPinned("my-skill", true);
    const record = await usage.getRecord("my-skill");
    expect(record.pinned).toBe(true);
  });

  it("clear removes a skill record", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-usage-"));
    const usage = new FilesystemSkillUsage(root);
    await usage.record("to-clear", "use");
    await usage.clear("to-clear");
    const record = await usage.getRecord("to-clear");
    expect(record.useCount).toBe(0);
  });

  it("listRecords returns all records", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-usage-"));
    const usage = new FilesystemSkillUsage(root);
    await usage.record("skill-a", "use");
    await usage.record("skill-b", "view");
    const records = await usage.listRecords();
    expect(records.length).toBe(2);
  });

  it("writes valid JSON to .usage.json", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-usage-"));
    const usage = new FilesystemSkillUsage(root);
    await usage.record("json-check", "use");
    const data = JSON.parse(await readFile(join(root, ".usage.json"), "utf8"));
    expect(data["json-check"].useCount).toBe(1);
  });

  it("never throws on record — returns gracefully for unknown state", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-usage-"));
    const usage = new FilesystemSkillUsage(root);
    await expect(usage.getRecord("never-seen")).resolves.toMatchObject({
      skillId: "never-seen",
      useCount: 0,
      state: "active",
    });
  });
});
