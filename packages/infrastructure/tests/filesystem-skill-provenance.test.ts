import { mkdtemp, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { FilesystemSkillProvenance } from "../src/index.js";

describe("FilesystemSkillProvenance", () => {
  it("defaults to user for unknown skills", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-prov-"));
    const prov = new FilesystemSkillProvenance(root);
    await expect(prov.get("unknown")).resolves.toBe("user");
  });

  it("marks agent and user correctly", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-prov-"));
    const prov = new FilesystemSkillProvenance(root);
    await prov.markAgent("agent-skill");
    await prov.markUser("user-skill");
    await expect(prov.get("agent-skill")).resolves.toBe("agent");
    await expect(prov.get("user-skill")).resolves.toBe("user");
  });

  it("persists across instances", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-prov-"));
    const prov1 = new FilesystemSkillProvenance(root);
    await prov1.markAgent("persisted");
    const prov2 = new FilesystemSkillProvenance(root);
    await expect(prov2.get("persisted")).resolves.toBe("agent");
  });

  it("clears provenance for a skill", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-prov-"));
    const prov = new FilesystemSkillProvenance(root);
    await prov.markAgent("to-clear");
    await prov.clear("to-clear");
    await expect(prov.get("to-clear")).resolves.toBe("user");
  });

  it("writes valid JSON to .provenance.json", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-prov-"));
    const prov = new FilesystemSkillProvenance(root);
    await prov.markAgent("json-check");
    const data = JSON.parse(await readFile(join(root, ".provenance.json"), "utf8"));
    expect(data["json-check"].createdBy).toBe("agent");
  });
});
