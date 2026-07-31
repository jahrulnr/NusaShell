import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { randomBytes } from "node:crypto";
import type {
  SkillState,
  SkillUsagePort,
  SkillUsageRecord,
  UsageBumpKind,
} from "@nusashell/application";

const USAGE_FILE = ".usage.json";

interface UsageMap {
  [skillId: string]: SkillUsageRecord;
}

function emptyRecord(skillId: string): SkillUsageRecord {
  return {
    skillId,
    useCount: 0,
    lastUsedAt: null,
    viewCount: 0,
    lastViewedAt: null,
    patchCount: 0,
    lastPatchedAt: null,
    createdAt: new Date().toISOString(),
    state: "active",
    pinned: false,
    archivedAt: null,
  };
}

export class FilesystemSkillUsage implements SkillUsagePort {
  constructor(private readonly root: string) {}

  async record(skillId: string, kind: UsageBumpKind): Promise<void> {
    const map = await this.load();
    const current = map[skillId] ?? emptyRecord(skillId);
    const now = new Date().toISOString();
    let next: SkillUsageRecord;
    switch (kind) {
      case "use":
        next = { ...current, useCount: current.useCount + 1, lastUsedAt: now };
        break;
      case "view":
        next = { ...current, viewCount: current.viewCount + 1, lastViewedAt: now };
        break;
      case "patch":
        next = { ...current, patchCount: current.patchCount + 1, lastPatchedAt: now };
        break;
    }
    map[skillId] = next;
    await this.persist(map);
  }

  async getRecord(skillId: string): Promise<SkillUsageRecord> {
    const map = await this.load();
    return map[skillId] ?? emptyRecord(skillId);
  }

  async listRecords(): Promise<readonly SkillUsageRecord[]> {
    const map = await this.load();
    return Object.values(map);
  }

  async setState(skillId: string, state: SkillState): Promise<void> {
    const map = await this.load();
    const current = map[skillId] ?? emptyRecord(skillId);
    map[skillId] = {
      ...current,
      state,
      ...(state === "archived" ? { archivedAt: new Date().toISOString() } : {}),
      ...(state === "active" || state === "stale" ? { archivedAt: null } : {}),
    };
    await this.persist(map);
  }

  async setPinned(skillId: string, pinned: boolean): Promise<void> {
    const map = await this.load();
    const current = map[skillId] ?? emptyRecord(skillId);
    map[skillId] = { ...current, pinned };
    await this.persist(map);
  }

  async clear(skillId: string): Promise<void> {
    const map = await this.load();
    if (!(skillId in map)) return;
    delete map[skillId];
    await this.persist(map);
  }

  private async load(): Promise<UsageMap> {
    try {
      const data = await readFile(resolve(this.root, USAGE_FILE), "utf8");
      return JSON.parse(data) as UsageMap;
    } catch {
      return {};
    }
  }

  private async persist(map: UsageMap): Promise<void> {
    await mkdir(this.root, { recursive: true });
    const target = resolve(this.root, USAGE_FILE);
    const staging = resolve(this.root, `.usage-${randomBytes(8).toString("hex")}.json`);
    await writeFile(staging, JSON.stringify(map, null, 2), "utf8");
    await rename(staging, target);
  }
}
