import { mkdir, readFile, readdir, rename, rm, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { randomBytes } from "node:crypto";
import type { PendingSkillWrite } from "@nusashell/contracts";

export type { PendingSkillWrite };

export class SkillApprovalStaging {
  constructor(private readonly root: string) {}

  async stage(skillId: string, action: PendingSkillWrite["action"], path: string, content: string): Promise<PendingSkillWrite> {
    const id = randomBytes(8).toString("hex");
    const pending: PendingSkillWrite = {
      id,
      skillId,
      action,
      path,
      content,
      createdAt: new Date().toISOString(),
    };
    await this.persist(id, pending);
    return pending;
  }

  async list(): Promise<readonly PendingSkillWrite[]> {
    const dir = resolve(this.root, ".pending");
    try {
      const files = await readdir(dir);
      const items: PendingSkillWrite[] = [];
      for (const file of files) {
        if (!file.endsWith(".json")) continue;
        const data = await readFile(resolve(dir, file), "utf8");
        items.push(JSON.parse(data) as PendingSkillWrite);
      }
      return items.sort((a, b) => a.createdAt.localeCompare(b.createdAt));
    } catch {
      return [];
    }
  }

  async get(id: string): Promise<PendingSkillWrite | null> {
    try {
      const data = await readFile(resolve(this.root, ".pending", `${id}.json`), "utf8");
      return JSON.parse(data) as PendingSkillWrite;
    } catch {
      return null;
    }
  }

  async remove(id: string): Promise<void> {
    await rm(resolve(this.root, ".pending", `${id}.json`), { force: true });
  }

  private async persist(id: string, pending: PendingSkillWrite): Promise<void> {
    const dir = resolve(this.root, ".pending");
    await mkdir(dir, { recursive: true });
    const target = resolve(dir, `${id}.json`);
    const staging = resolve(dir, `.tmp-${id}.json`);
    await writeFile(staging, JSON.stringify(pending, null, 2), "utf8");
    await rename(staging, target);
  }
}
