import { mkdtemp, readFile, rm, writeFile, mkdir } from "node:fs/promises";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";
import { afterEach, describe, expect, it } from "vitest";
import { ensureBuiltinSkill, seedBuiltinSkills, markBuiltinSkillDeleted, unmarkBuiltinSkillDeleted } from "../src/composers/skills-runtime.js";
import { existsSync } from "node:fs";

const __dirname = fileURLToPath(new URL(".", import.meta.url));
const skillsSource = resolve(__dirname, "../../../resources/agent/skills");

const roots: string[] = [];
afterEach(async () => { await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true }))); });

describe("ensureBuiltinSkill", () => {
  it("copies mcp-creator and marks builtin provenance", async () => {
    const destination = await mkdtemp(join(tmpdir(), "nusashell-skills-"));
    roots.push(destination);
    ensureBuiltinSkill(skillsSource, destination, "mcp-creator");
    expect((await readFile(join(destination, "mcp-creator", "SKILL.md"), "utf8"))).toContain("mcp-creator");
    const provenance = JSON.parse(await readFile(join(destination, ".provenance.json"), "utf8"));
    expect(provenance["mcp-creator"].createdBy).toBe("builtin");
  });

  it("seeds skill-creator as another builtin package", async () => {
    const destination = await mkdtemp(join(tmpdir(), "nusashell-skills-"));
    roots.push(destination);
    ensureBuiltinSkill(skillsSource, destination, "skill-creator");
    expect((await readFile(join(destination, "skill-creator", "SKILL.md"), "utf8"))).toContain("skill-creator");
    const provenance = JSON.parse(await readFile(join(destination, ".provenance.json"), "utf8"));
    expect(provenance["skill-creator"].createdBy).toBe("builtin");
  });

  it("does not overwrite an existing non-builtin skill", async () => {
    const destination = await mkdtemp(join(tmpdir(), "nusashell-skills-"));
    roots.push(destination);
    await mkdir(join(destination, "mcp-creator"), { recursive: true });
    await writeFile(join(destination, "mcp-creator", "SKILL.md"), "user content", "utf8");
    ensureBuiltinSkill(skillsSource, destination, "mcp-creator");
    expect(await readFile(join(destination, "mcp-creator", "SKILL.md"), "utf8")).toBe("user content");
  });

  it("seeds a skill without a VERSION file (no version gate)", async () => {
    const source = await mkdtemp(join(tmpdir(), "nusashell-skill-src-"));
    roots.push(source);
    await mkdir(join(source, "versionless-skill"), { recursive: true });
    await writeFile(join(source, "versionless-skill", "SKILL.md"),
      "---\nname: versionless-skill\ndescription: A skill with no VERSION file.\n---\n# Versionless\n", "utf8");
    const destination = await mkdtemp(join(tmpdir(), "nusashell-skills-"));
    roots.push(destination);
    ensureBuiltinSkill(source, destination, "versionless-skill");
    expect(await readFile(join(destination, "versionless-skill", "SKILL.md"), "utf8")).toContain("Versionless");
  });
});

describe("seedBuiltinSkills", () => {
  it("seeds every skill folder with a SKILL.md from the real resources tree", async () => {
    const destination = await mkdtemp(join(tmpdir(), "nusashell-skills-"));
    roots.push(destination);
    seedBuiltinSkills(skillsSource, destination);
    const provenance = JSON.parse(await readFile(join(destination, ".provenance.json"), "utf8"));
    expect(provenance["mcp-creator"].createdBy).toBe("builtin");
    expect(provenance["skill-creator"].createdBy).toBe("builtin");
    expect(provenance["sdlc-role-skills"].createdBy).toBe("builtin");
  });

  it("does not error when source root is undefined", async () => {
    const destination = await mkdtemp(join(tmpdir(), "nusashell-skills-"));
    roots.push(destination);
    expect(() => seedBuiltinSkills(undefined, destination)).not.toThrow();
  });

  it("does not error when source root does not exist", async () => {
    const destination = await mkdtemp(join(tmpdir(), "nusashell-skills-"));
    roots.push(destination);
    expect(() => seedBuiltinSkills(join(tmpdir(), "nonexistent-skills-root-xyz"), destination)).not.toThrow();
  });

  it("does not error when source root is empty", async () => {
    const source = await mkdtemp(join(tmpdir(), "nusashell-empty-src-"));
    roots.push(source);
    const destination = await mkdtemp(join(tmpdir(), "nusashell-skills-"));
    roots.push(destination);
    expect(() => seedBuiltinSkills(source, destination)).not.toThrow();
  });

  it("skips folders without a SKILL.md", async () => {
    const source = await mkdtemp(join(tmpdir(), "nusashell-skill-src-"));
    roots.push(source);
    await mkdir(join(source, "no-skill-md"), { recursive: true });
    await mkdir(join(source, "valid-skill"), { recursive: true });
    await writeFile(join(source, "valid-skill", "SKILL.md"),
      "---\nname: valid-skill\ndescription: Valid.\n---\n# Valid\n", "utf8");
    const destination = await mkdtemp(join(tmpdir(), "nusashell-skills-"));
    roots.push(destination);
    seedBuiltinSkills(source, destination);
    const provenance = JSON.parse(await readFile(join(destination, ".provenance.json"), "utf8"));
    expect(provenance["valid-skill"].createdBy).toBe("builtin");
    expect(provenance["no-skill-md"]).toBeUndefined();
  });

  it("skips folders that do not match the skill-id regex", async () => {
    const source = await mkdtemp(join(tmpdir(), "nusashell-skill-src-"));
    roots.push(source);
    await mkdir(join(source, "Bad_Name"), { recursive: true });
    await writeFile(join(source, "Bad_Name", "SKILL.md"), "invalid", "utf8");
    const destination = await mkdtemp(join(tmpdir(), "nusashell-skills-"));
    roots.push(destination);
    expect(() => seedBuiltinSkills(source, destination)).not.toThrow();
  });
});

describe("builtin skill deletion tracking", () => {
  it("does not re-seed a user-deleted builtin skill", async () => {
    const source = await mkdtemp(join(tmpdir(), "nusashell-skill-src-"));
    roots.push(source);
    await mkdir(join(source, "deletable-skill"), { recursive: true });
    await writeFile(join(source, "deletable-skill", "SKILL.md"),
      "---\nname: deletable-skill\ndescription: Can be deleted.\n---\n# Deletable\n", "utf8");
    const destination = await mkdtemp(join(tmpdir(), "nusashell-skills-"));
    roots.push(destination);

    seedBuiltinSkills(source, destination);
    expect(existsSync(join(destination, "deletable-skill", "SKILL.md"))).toBe(true);

    // User deletes the skill and marks it as intentionally deleted.
    await rm(join(destination, "deletable-skill"), { recursive: true, force: true });
    markBuiltinSkillDeleted(destination, "deletable-skill");

    // Re-seed should NOT resurrect the deleted skill.
    seedBuiltinSkills(source, destination);
    expect(existsSync(join(destination, "deletable-skill"))).toBe(false);
  });

  it("re-seeds a skill after unmarking its deletion", async () => {
    const source = await mkdtemp(join(tmpdir(), "nusashell-skill-src-"));
    roots.push(source);
    await mkdir(join(source, "restorable-skill"), { recursive: true });
    await writeFile(join(source, "restorable-skill", "SKILL.md"),
      "---\nname: restorable-skill\ndescription: Can be restored.\n---\n# Restorable\n", "utf8");
    const destination = await mkdtemp(join(tmpdir(), "nusashell-skills-"));
    roots.push(destination);

    seedBuiltinSkills(source, destination);
    await rm(join(destination, "restorable-skill"), { recursive: true, force: true });
    markBuiltinSkillDeleted(destination, "restorable-skill");
    seedBuiltinSkills(source, destination);
    expect(existsSync(join(destination, "restorable-skill"))).toBe(false);

    // User changes their mind and restores.
    unmarkBuiltinSkillDeleted(destination, "restorable-skill");
    seedBuiltinSkills(source, destination);
    expect(existsSync(join(destination, "restorable-skill", "SKILL.md"))).toBe(true);
  });
});

describe("builtin skill orphan cleanup", () => {
  it("removes orphan builtin skills no longer in source", async () => {
    const source = await mkdtemp(join(tmpdir(), "nusashell-skill-src-"));
    roots.push(source);
    await mkdir(join(source, "kept-skill"), { recursive: true });
    await writeFile(join(source, "kept-skill", "SKILL.md"),
      "---\nname: kept-skill\ndescription: Kept.\n---\n# Kept\n", "utf8");
    await mkdir(join(source, "removed-skill"), { recursive: true });
    await writeFile(join(source, "removed-skill", "SKILL.md"),
      "---\nname: removed-skill\ndescription: Removed in new version.\n---\n# Removed\n", "utf8");
    const destination = await mkdtemp(join(tmpdir(), "nusashell-skills-"));
    roots.push(destination);

    // First seed installs both skills.
    seedBuiltinSkills(source, destination);
    expect(existsSync(join(destination, "removed-skill", "SKILL.md"))).toBe(true);

    // Simulate a new version that removed "removed-skill" from resources.
    await rm(join(source, "removed-skill"), { recursive: true, force: true });
    seedBuiltinSkills(source, destination);

    expect(existsSync(join(destination, "kept-skill", "SKILL.md"))).toBe(true);
    expect(existsSync(join(destination, "removed-skill"))).toBe(false);
  });

  it("preserves user/agent-owned skills during orphan cleanup", async () => {
    const source = await mkdtemp(join(tmpdir(), "nusashell-skill-src-"));
    roots.push(source);
    await mkdir(join(source, "builtin-skill"), { recursive: true });
    await writeFile(join(source, "builtin-skill", "SKILL.md"),
      "---\nname: builtin-skill\ndescription: Builtin.\n---\n# Builtin\n", "utf8");
    const destination = await mkdtemp(join(tmpdir(), "nusashell-skills-"));
    roots.push(destination);

    // Seed the builtin skill.
    seedBuiltinSkills(source, destination);

    // User creates their own skill in the same root.
    await mkdir(join(destination, "my-skill"), { recursive: true });
    await writeFile(join(destination, "my-skill", "SKILL.md"),
      "---\nname: my-skill\ndescription: User-owned.\n---\n# Mine\n", "utf8");

    // Remove the builtin from source and re-seed.
    await rm(join(source, "builtin-skill"), { recursive: true, force: true });
    seedBuiltinSkills(source, destination);

    // User skill must survive orphan cleanup.
    expect(existsSync(join(destination, "my-skill", "SKILL.md"))).toBe(true);
  });
});
