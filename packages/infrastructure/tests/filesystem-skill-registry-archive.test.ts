import { mkdtemp, mkdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { FilesystemSkillRegistry } from "../src/index.js";

const SKILL_MD = (id: string) =>
  `---\nname: ${id}\ndescription: Test skill\n---\n\nBody\n`;

async function createSkill(root: string, id: string) {
  const dir = join(root, id);
  await mkdir(dir, { recursive: true });
  await writeFile(join(dir, "SKILL.md"), SKILL_MD(id), "utf8");
}

describe("FilesystemSkillRegistry archive/restore", () => {
  it("archive moves skill to .archive/ directory", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-archive-"));
    const registry = new FilesystemSkillRegistry(root);
    await createSkill(root, "test-skill");

    await registry.archive("test-skill");

    const archived = await registry.listArchived();
    expect(archived).toHaveLength(1);
    expect(archived[0]!.id).toBe("test-skill");
    expect(archived[0]!.archivedAt).toBeTruthy();

    const active = await registry.list();
    expect(active).toHaveLength(0);
  });

  it("restore moves skill back from .archive/", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-archive-"));
    const registry = new FilesystemSkillRegistry(root);
    await createSkill(root, "test-skill");
    await registry.archive("test-skill");

    await registry.restore("test-skill");

    const active = await registry.list();
    expect(active).toHaveLength(1);
    expect(active[0]!.id).toBe("test-skill");

    const archived = await registry.listArchived();
    expect(archived).toHaveLength(0);
  });

  it("archive throws if skill not found", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-archive-"));
    const registry = new FilesystemSkillRegistry(root);

    await expect(registry.archive("nope")).rejects.toThrow("Skill not found");
  });

  it("restore throws if archived skill not found", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-archive-"));
    const registry = new FilesystemSkillRegistry(root);

    await expect(registry.restore("nope")).rejects.toThrow("Archived skill not found");
  });

  it("cleans an active duplicate when the skill is already archived", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-archive-"));
    const registry = new FilesystemSkillRegistry(root);
    await createSkill(root, "test-skill");
    await registry.archive("test-skill");

    await createSkill(root, "test-skill");
    await expect(registry.archive("test-skill")).resolves.toBeUndefined();
    expect(await registry.list()).toHaveLength(0);
    expect(await registry.listArchived()).toHaveLength(1);
  });

  it("restore throws if skill already exists in active root", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-archive-"));
    const registry = new FilesystemSkillRegistry(root);
    await createSkill(root, "test-skill");
    await registry.archive("test-skill");
    await createSkill(root, "test-skill");

    await expect(registry.restore("test-skill")).rejects.toThrow("Skill already exists");
  });

  it("listArchived returns empty when no .archive/ directory exists", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-archive-"));
    const registry = new FilesystemSkillRegistry(root);

    const archived = await registry.listArchived();
    expect(archived).toEqual([]);
  });

  it("archived skill content is preserved", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-archive-"));
    const registry = new FilesystemSkillRegistry(root);
    await createSkill(root, "test-skill");
    await registry.archive("test-skill");
    await registry.restore("test-skill");

    const result = await registry.read("test-skill", "SKILL.md");
    expect(result.content).toContain("Body");
  });
});
