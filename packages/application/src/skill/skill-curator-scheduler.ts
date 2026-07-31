import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { randomBytes } from "node:crypto";
import type { SkillCuratorService, CuratorResult, CuratorSettings } from "./index.js";
import type { LoggerPort } from "../plugin/ports/logger.port.js";

const STATE_FILE = ".curator-state.json";
const MS_PER_HOUR = 60 * 60 * 1000;

export interface CuratorSchedulerSettings {
  readonly enabled: boolean;
  readonly intervalHours: number;
  readonly paused: boolean;
}

export const DEFAULT_SCHEDULER_SETTINGS: CuratorSchedulerSettings = {
  enabled: true,
  intervalHours: 168,
  paused: false,
};

interface PersistedState {
  readonly lastRunAt: string | null;
}

export interface SkillCuratorSchedulerDeps {
  readonly curator: SkillCuratorService;
  readonly stateRoot: string;
  readonly logger?: LoggerPort;
  readonly now?: () => Date;
}

export class SkillCuratorScheduler {
  private settings: CuratorSchedulerSettings = DEFAULT_SCHEDULER_SETTINGS;
  private lastRunAt: string | null = null;
  private runInFlight = false;

  constructor(private readonly deps: SkillCuratorSchedulerDeps) {}

  async initialize(): Promise<void> {
    await this.loadState();
  }

  configure(settings: Partial<CuratorSchedulerSettings>): void {
    this.settings = { ...this.settings, ...settings };
  }

  configureCurator(settings: Partial<CuratorSettings>): void {
    this.deps.curator.configure(settings);
  }

  getSettings(): CuratorSchedulerSettings {
    return this.settings;
  }

  getCuratorSettings(): CuratorSettings {
    return this.deps.curator.getSettings();
  }

  getStatus(): { lastRunAt: string | null; running: boolean } {
    return { lastRunAt: this.lastRunAt, running: this.runInFlight };
  }

  async tick(): Promise<CuratorResult | null> {
    if (!this.settings.enabled || this.settings.paused || this.runInFlight) return null;
    const now = (this.deps.now ?? (() => new Date()))();
    if (this.lastRunAt) {
      const elapsed = now.getTime() - new Date(this.lastRunAt).getTime();
      if (elapsed < this.settings.intervalHours * MS_PER_HOUR) return null;
    }
    return this.runInternal(false, now);
  }

  async runManual(dryRun = false): Promise<CuratorResult> {
    const now = (this.deps.now ?? (() => new Date()))();
    return (await this.runInternal(dryRun, now)) ?? { dryRun, changes: [] };
  }

  private async runInternal(dryRun: boolean, now: Date): Promise<CuratorResult | null> {
    if (this.runInFlight) return null;
    this.runInFlight = true;
    try {
      const result = await this.deps.curator.run(dryRun);
      this.lastRunAt = now.toISOString();
      await this.persistState();
      return result;
    } catch (error) {
      this.deps.logger?.warn("skill curator run failed: %s", error instanceof Error ? error.message : String(error));
      return null;
    } finally {
      this.runInFlight = false;
    }
  }

  private async loadState(): Promise<void> {
    try {
      const data = await readFile(resolve(this.deps.stateRoot, STATE_FILE), "utf8");
      const state = JSON.parse(data) as PersistedState;
      this.lastRunAt = state.lastRunAt ?? null;
    } catch {
      this.lastRunAt = null;
    }
  }

  private async persistState(): Promise<void> {
    await mkdir(this.deps.stateRoot, { recursive: true });
    const target = resolve(this.deps.stateRoot, STATE_FILE);
    const staging = resolve(this.deps.stateRoot, `.curator-state-${randomBytes(8).toString("hex")}.json`);
    const state: PersistedState = { lastRunAt: this.lastRunAt };
    await writeFile(staging, JSON.stringify(state, null, 2), "utf8");
    await rename(staging, target);
  }
}
