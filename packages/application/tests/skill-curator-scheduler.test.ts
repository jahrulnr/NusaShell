import { mkdtemp, mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { randomBytes } from "node:crypto";
import { describe, expect, it } from "vitest";
import {
  SkillCuratorService,
  SkillCuratorScheduler,
  DEFAULT_SCHEDULER_SETTINGS,
  type SkillCuratorDeps,
  type CuratorStateStorePort,
} from "../src/index.js";

function makeCurator(): SkillCuratorService {
  return new SkillCuratorService({
    registry: { list: async () => [], archive: async () => {} },
    provenance: { get: async () => "agent" },
    usage: { getRecord: async () => ({ skillId: "x", useCount: 0, viewCount: 0, patchCount: 0, lastUsedAt: null, lastViewedAt: null, lastPatchedAt: null, state: "active", pinned: false, archivedAt: null, createdAt: new Date().toISOString() }), setState: async () => {}, setPinned: async () => {} },
  } as unknown as SkillCuratorDeps);
}

const STATE_FILE = ".curator-state.json";

/** Test-only CuratorStateStorePort backed by the real filesystem (temp dir). */
class TestCuratorStateStore implements CuratorStateStorePort {
  constructor(private readonly root: string) {}

  async load(): Promise<{ lastRunAt: string | null }> {
    try {
      const data = await readFile(resolve(this.root, STATE_FILE), "utf8");
      const state = JSON.parse(data) as { lastRunAt?: string | null };
      return { lastRunAt: state.lastRunAt ?? null };
    } catch {
      return { lastRunAt: null };
    }
  }

  async save(state: { lastRunAt: string | null }): Promise<void> {
    await mkdir(this.root, { recursive: true });
    const target = resolve(this.root, STATE_FILE);
    const staging = resolve(this.root, `.curator-state-${randomBytes(8).toString("hex")}.json`);
    await writeFile(staging, JSON.stringify(state, null, 2), "utf8");
    await rename(staging, target);
  }
}

describe("SkillCuratorScheduler", () => {
  it("uses default settings", () => {
    const curator = makeCurator();
    const scheduler = new SkillCuratorScheduler({ curator, stateStore: new TestCuratorStateStore("/tmp") });
    expect(scheduler.getSettings()).toEqual(DEFAULT_SCHEDULER_SETTINGS);
  });

  it("interval gate blocks tick before interval elapses", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-sched-"));
    const now = new Date("2025-01-01T00:00:00Z");
    const curator = makeCurator();
    const scheduler = new SkillCuratorScheduler({ curator, stateStore: new TestCuratorStateStore(root), now: () => now });
    await scheduler.initialize();

    await scheduler.runManual(false);
    const tick = await scheduler.tick();
    expect(tick).toBeNull();
  });

  it("tick runs after interval elapses", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-sched-"));
    let now = new Date("2025-01-01T00:00:00Z");
    const curator = makeCurator();
    const scheduler = new SkillCuratorScheduler({ curator, stateStore: new TestCuratorStateStore(root), now: () => now });
    await scheduler.initialize();

    await scheduler.runManual(false);
    now = new Date("2025-01-08T00:00:01Z");
    const tick = await scheduler.tick();
    expect(tick).not.toBeNull();
  });

  it("paused blocks automatic tick", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-sched-"));
    let now = new Date("2025-01-01T00:00:00Z");
    const curator = makeCurator();
    const scheduler = new SkillCuratorScheduler({ curator, stateStore: new TestCuratorStateStore(root), now: () => now });
    await scheduler.initialize();
    scheduler.configure({ paused: true });

    now = new Date("2025-02-01T00:00:00Z");
    const tick = await scheduler.tick();
    expect(tick).toBeNull();
  });

  it("manual run bypasses interval and paused", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-sched-"));
    const now = new Date("2025-01-01T00:00:00Z");
    const curator = makeCurator();
    const scheduler = new SkillCuratorScheduler({ curator, stateStore: new TestCuratorStateStore(root), now: () => now });
    await scheduler.initialize();
    scheduler.configure({ paused: true });

    const result = await scheduler.runManual(true);
    expect(result).not.toBeNull();
    expect(result?.dryRun).toBe(true);
  });

  it("manual run works when not paused", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-sched-"));
    const now = new Date("2025-01-01T00:00:00Z");
    const curator = makeCurator();
    const scheduler = new SkillCuratorScheduler({ curator, stateStore: new TestCuratorStateStore(root), now: () => now });
    await scheduler.initialize();

    const result = await scheduler.runManual(true);
    expect(result).not.toBeNull();
    expect(result?.dryRun).toBe(true);
  });

  it("persists lastRunAt across instances", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-sched-"));
    const now = new Date("2025-01-01T00:00:00Z");
    const curator = makeCurator();
    const scheduler1 = new SkillCuratorScheduler({ curator, stateStore: new TestCuratorStateStore(root), now: () => now });
    await scheduler1.initialize();
    await scheduler1.runManual(false);
    expect(scheduler1.getStatus().lastRunAt).not.toBeNull();

    const scheduler2 = new SkillCuratorScheduler({ curator, stateStore: new TestCuratorStateStore(root), now: () => now });
    await scheduler2.initialize();
    expect(scheduler2.getStatus().lastRunAt).not.toBeNull();
  });

  it("disabled scheduler does not tick", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-sched-"));
    const now = new Date("2025-06-01T00:00:00Z");
    const curator = makeCurator();
    const scheduler = new SkillCuratorScheduler({ curator, stateStore: new TestCuratorStateStore(root), now: () => now });
    await scheduler.initialize();
    scheduler.configure({ enabled: false });

    const tick = await scheduler.tick();
    expect(tick).toBeNull();
  });
});
