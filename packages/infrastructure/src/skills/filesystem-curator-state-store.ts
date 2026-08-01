import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { randomBytes } from "node:crypto";
import type { CuratorStateStorePort } from "@nusashell/application";

const STATE_FILE = ".curator-state.json";

/**
 * Filesystem implementation of {@link CuratorStateStorePort}. Persists the
 * curator scheduler's last-run timestamp via an atomic staging-file + rename.
 */
export class FilesystemCuratorStateStore implements CuratorStateStorePort {
  constructor(private readonly stateRoot: string) {}

  async load(): Promise<{ lastRunAt: string | null }> {
    try {
      const data = await readFile(resolve(this.stateRoot, STATE_FILE), "utf8");
      const state = JSON.parse(data) as { lastRunAt?: string | null };
      return { lastRunAt: state.lastRunAt ?? null };
    } catch {
      return { lastRunAt: null };
    }
  }

  async save(state: { lastRunAt: string | null }): Promise<void> {
    await mkdir(this.stateRoot, { recursive: true });
    const target = resolve(this.stateRoot, STATE_FILE);
    const staging = resolve(this.stateRoot, `.curator-state-${randomBytes(8).toString("hex")}.json`);
    await writeFile(staging, JSON.stringify(state, null, 2), "utf8");
    await rename(staging, target);
  }
}
