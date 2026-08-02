import { mkdtemp, readFile, rm, writeFile, mkdir } from "node:fs/promises";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";
import { afterEach, describe, expect, it } from "vitest";
import { ensureBuiltinSkill } from "../src/composers/skills-runtime.js";

const __dirname = fileURLToPath(new URL(".", import.meta.url));
const skillsSource = resolve(__dirname, "../../../resources/agent/skills");

const roots: string[] = [];
afterEach(async () => { await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true }))); });

describe("builtin skill seed", () => {
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
});
