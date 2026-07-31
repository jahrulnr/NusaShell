import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { SkillApprovalStaging } from "../src/index.js";

describe("SkillApprovalStaging", () => {
  it("stages a pending write and lists it", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-staging-"));
    const staging = new SkillApprovalStaging(root);
    const pending = await staging.stage("my-skill", "create", "SKILL.md", "---\nname: my-skill\n---\n");
    expect(pending).toMatchObject({ skillId: "my-skill", action: "create", path: "SKILL.md" });
    const list = await staging.list();
    expect(list).toHaveLength(1);
    expect(list[0]!.id).toBe(pending.id);
  });

  it("gets a staged write by id", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-staging-"));
    const staging = new SkillApprovalStaging(root);
    const pending = await staging.stage("skill", "edit", "SKILL.md", "content");
    const found = await staging.get(pending.id);
    expect(found).toEqual(pending);
  });

  it("returns null for unknown id", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-staging-"));
    const staging = new SkillApprovalStaging(root);
    await expect(staging.get("nonexistent")).resolves.toBeNull();
  });

  it("removes a staged write", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-staging-"));
    const staging = new SkillApprovalStaging(root);
    const pending = await staging.stage("skill", "delete", "SKILL.md", "");
    await staging.remove(pending.id);
    await expect(staging.list()).resolves.toEqual([]);
  });

  it("lists multiple pending writes sorted by createdAt", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-staging-"));
    const staging = new SkillApprovalStaging(root);
    await staging.stage("a", "create", "SKILL.md", "a");
    await staging.stage("b", "edit", "SKILL.md", "b");
    const list = await staging.list();
    expect(list).toHaveLength(2);
  });

  it("returns empty list when no pending writes exist", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-staging-"));
    const staging = new SkillApprovalStaging(root);
    await expect(staging.list()).resolves.toEqual([]);
  });
});
