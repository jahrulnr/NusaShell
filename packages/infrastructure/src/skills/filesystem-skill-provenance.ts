import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { randomBytes } from "node:crypto";
import type { SkillOrigin, SkillProvenancePort } from "@nusashell/application";

const PROVENANCE_FILE = ".provenance.json";

interface ProvenanceMap {
  [skillId: string]: { createdBy: SkillOrigin; createdAt: string };
}

export class FilesystemSkillProvenance implements SkillProvenancePort {
  constructor(private readonly root: string) {}

  async get(skillId: string): Promise<SkillOrigin> {
    const map = await this.load();
    return map[skillId]?.createdBy ?? "user";
  }

  async markAgent(skillId: string): Promise<void> {
    await this.set(skillId, "agent");
  }

  async markUser(skillId: string): Promise<void> {
    await this.set(skillId, "user");
  }

  async markBuiltin(skillId: string): Promise<void> {
    await this.set(skillId, "builtin");
  }

  async clear(skillId: string): Promise<void> {
    const map = await this.load();
    if (!(skillId in map)) return;
    delete map[skillId];
    await this.persist(map);
  }

  private async set(skillId: string, origin: SkillOrigin): Promise<void> {
    const map = await this.load();
    map[skillId] = { createdBy: origin, createdAt: new Date().toISOString() };
    await this.persist(map);
  }

  private async load(): Promise<ProvenanceMap> {
    try {
      const data = await readFile(resolve(this.root, PROVENANCE_FILE), "utf8");
      return JSON.parse(data) as ProvenanceMap;
    } catch {
      return {};
    }
  }

  private async persist(map: ProvenanceMap): Promise<void> {
    await mkdir(this.root, { recursive: true });
    const target = resolve(this.root, PROVENANCE_FILE);
    const staging = resolve(this.root, `.provenance-${randomBytes(8).toString("hex")}.json`);
    await writeFile(staging, JSON.stringify(map, null, 2), "utf8");
    await rename(staging, target);
  }
}
