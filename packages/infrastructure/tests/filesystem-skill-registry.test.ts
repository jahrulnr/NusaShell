import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import AdmZip from "adm-zip";
import { describe, expect, it } from "vitest";
import { FilesystemSkillRegistry } from "../src/index.js";

function skillArchive(path: string, entries: Readonly<Record<string, string>>): void {
  const zip = new AdmZip();
  for (const [name, content] of Object.entries(entries)) zip.addFile(name, Buffer.from(content));
  zip.writeZip(path);
}

async function traversalArchive(path: string): Promise<void> {
  skillArchive(path, {
    "SKILL.md": "---\nname: unsafe\ndescription: Unsafe archive.\n---\n",
    "aa/escape": "escape",
  });
  const archive = await readFile(path);
  const safeName = Buffer.from("aa/escape");
  const unsafeName = Buffer.from("../escape");
  let cursor = 0;
  while ((cursor = archive.indexOf(safeName, cursor)) !== -1) {
    unsafeName.copy(archive, cursor);
    cursor += unsafeName.length;
  }
  await writeFile(path, archive);
}

describe("FilesystemSkillRegistry", () => {
  it("installs a wrapped .skill archive and supports bounded discovery, reading, editing, and deletion", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-skills-"));
    const archive = join(root, "review.skill");
    skillArchive(archive, {
      "review-package/SKILL.md": "---\nname: code-review\ndescription: Review code changes carefully.\n---\n\n# Code Review\n",
      "review-package/references/checklist.md": "# Checklist\n\nCheck correctness and security.",
      "review-package/assets/logo.bin": "\u0000binary",
    });
    const registry = new FilesystemSkillRegistry(join(root, "managed"));

    const installed = await registry.installFromArchive(archive);
    expect(installed).toMatchObject({ id: "code-review", name: "code-review", fileCount: 3 });
    expect(installed.files).toContainEqual(expect.objectContaining({
      path: "references/checklist.md",
      type: "file",
      editable: true,
    }));
    expect(installed.files).toContainEqual(expect.objectContaining({
      path: "assets/logo.bin",
      editable: false,
    }));
    await expect(registry.list()).resolves.toHaveLength(1);
    await expect(registry.search("carefully")).resolves.toHaveLength(1);
    await expect(registry.read("code-review")).resolves.toMatchObject({
      path: "SKILL.md",
      content: expect.stringContaining("# Code Review"),
      truncated: false,
    });
    await expect(registry.read("code-review", "references/checklist.md", 0, 12)).resolves.toMatchObject({
      content: "# Checklist\n",
      truncated: true,
      nextOffset: 12,
    });

    await registry.write("code-review", "references/checklist.md", "# Updated\n");
    expect(await readFile(join(root, "managed", "code-review", "references", "checklist.md"), "utf8")).toBe("# Updated\n");
    await expect(registry.write("code-review", "SKILL.md", [
      "---",
      "name: renamed",
      "description: Review code changes carefully.",
      "---",
      "",
    ].join("\n"))).rejects.toThrow(/name must remain code-review/i);
    await expect(registry.get("code-review")).resolves.toMatchObject({ id: "code-review" });
    await registry.delete("code-review");
    await expect(registry.list()).resolves.toEqual([]);
  });

  it("rejects traversal entries and paths outside the managed skill", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-skills-"));
    const archive = join(root, "unsafe.zip");
    await traversalArchive(archive);
    const registry = new FilesystemSkillRegistry(join(root, "managed"));

    await expect(registry.installFromArchive(archive)).rejects.toThrow(/unsafe archive path/i);
    await expect(registry.read("unsafe", "../escape.txt")).rejects.toThrow(/invalid skill path/i);

    const ambiguous = join(root, "ambiguous.skill");
    skillArchive(ambiguous, {
      "wrapped/SKILL.md": "---\nname: ambiguous\ndescription: Ambiguous archive.\n---\n",
      "outside.txt": "not part of the package",
    });
    await expect(registry.installFromArchive(ambiguous)).rejects.toThrow(/outside its package root/i);
  });
});
