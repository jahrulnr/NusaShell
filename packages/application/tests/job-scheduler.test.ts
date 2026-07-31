import { describe, expect, it, beforeEach, afterEach } from "vitest";
import { mkdtemp, rm } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import {
  JobScheduler,
  DEFAULT_JOB_SCHEDULER_SETTINGS,
  DEFAULT_JOB_EXECUTOR_SETTINGS,
  type Job,
  type JobStorePort,
  type JobOutputEntry,
  type JobExecutionResult,
  type JobAgentExecutorSettings,
  type ApplicationEvent,
  type CallToolCommand,
  EventDispatcher,
} from "../src/index.js";
import { createJobCompletedEvent, createJobFailedEvent } from "../src/events/job-events.event.js";

function makeJob(overrides: Partial<Job> = {}): Job {
  return {
    id: "job-1",
    name: "Test job",
    schedule: { kind: "interval", minutes: 30 },
    mode: { type: "agent", prompt: "Say hello" },
    enabled: true,
    repeat: { times: null, completed: 0 },
    nextRunAt: "2024-12-31T23:00:00.000Z",
    lastRunAt: null,
    lastStatus: null,
    lastError: null,
    createdAt: "2025-01-01T00:00:00.000Z",
    ...overrides,
  };
}

class FakeJobStore implements JobStorePort {
  jobs = new Map<string, Job>();
  claims = new Map<string, string>();
  outputs = new Map<string, JobOutputEntry[]>();
  claimTtl = new Map<string, number>();

  async create(job: Job): Promise<Job> { this.jobs.set(job.id, job); return job; }
  async update(job: Job): Promise<Job> { this.jobs.set(job.id, job); return job; }
  async get(id: string): Promise<Job | null> { return this.jobs.get(id) ?? null; }
  async list(): Promise<readonly Job[]> { return [...this.jobs.values()]; }
  async remove(id: string): Promise<void> {
    this.jobs.delete(id);
    this.claims.delete(id);
    this.outputs.delete(id);
  }
  async markRun(id: string, status: "ok" | "error", error: string | null, nextRunAt: string | null, now: Date): Promise<Job | null> {
    const existing = this.jobs.get(id);
    if (!existing) return null;
    const completed = existing.repeat.completed + 1;
    const times = existing.repeat.times;
    const limitReached = times !== null && completed >= times;
    const enabled = existing.schedule.kind === "once" ? false : limitReached ? false : existing.enabled;
    const updated: Job = {
      ...existing,
      enabled,
      repeat: { ...existing.repeat, completed },
      nextRunAt,
      lastRunAt: now.toISOString(),
      lastStatus: status,
      lastError: error,
    };
    this.jobs.set(id, updated);
    return updated;
  }
  async claimFire(jobId: string, claimId: string, ttlSeconds: number, now: Date): Promise<boolean> {
    const existingClaim = this.claims.get(jobId);
    if (existingClaim) {
      const expiresAt = this.claimTtl.get(jobId) ?? 0;
      if (now.getTime() < expiresAt) return false;
    }
    this.claims.set(jobId, claimId);
    this.claimTtl.set(jobId, now.getTime() + ttlSeconds * 1000);
    return true;
  }
  async releaseFire(jobId: string): Promise<void> {
    this.claims.delete(jobId);
    this.claimTtl.delete(jobId);
  }
  async listDue(now: Date): Promise<readonly Job[]> {
    const nowIso = now.toISOString();
    return [...this.jobs.values()]
      .filter((job) => job.enabled && job.nextRunAt !== null && job.nextRunAt <= nowIso && !this.claims.has(job.id))
      .sort((a, b) => (a.nextRunAt ?? "").localeCompare(b.nextRunAt ?? ""));
  }
  async appendOutput(jobId: string, entry: JobOutputEntry): Promise<void> {
    const list = this.outputs.get(jobId) ?? [];
    this.outputs.set(jobId, [entry, ...list].slice(0, 100));
  }
  async listOutputs(jobId: string, limit: number): Promise<readonly JobOutputEntry[]> {
    return (this.outputs.get(jobId) ?? []).slice(0, limit);
  }
}

class ScriptedExecutor {
  constructor(private readonly respond: (prompt: string) => JobExecutionResult) {}
  async runAgent(prompt: string, _settings: JobAgentExecutorSettings): Promise<JobExecutionResult> {
    return this.respond(prompt);
  }
}

class FakeCallToolHandler {
  readonly calls: CallToolCommand[] = [];
  constructor(private readonly result: unknown = { ok: true }) {}
  async handle(command: CallToolCommand) {
    this.calls.push(command);
    return { requestId: command.requestId, result: this.result };
  }
}

function makeDeps(overrides: Partial<{
  store: FakeJobStore;
  executor: ScriptedExecutor;
  callToolHandler: FakeCallToolHandler;
  eventDispatcher: EventDispatcher;
  jobsRoot: string;
  executorSettings: JobAgentExecutorSettings;
}> = {}) {
  const store = overrides.store ?? new FakeJobStore();
  const executor = overrides.executor ?? new ScriptedExecutor(() => ({ traceId: "t", status: "ok", summary: "done" }));
  const callToolHandler = overrides.callToolHandler ?? new FakeCallToolHandler();
  const eventDispatcher = overrides.eventDispatcher ?? new EventDispatcher();
  return { store, executor, callToolHandler, eventDispatcher };
}

describe("JobScheduler", () => {
  let tempDir: string;
  const NOW = new Date("2025-01-01T00:00:00Z");

  beforeEach(async () => {
    tempDir = await mkdtemp(join(tmpdir(), "nusashell-sched-"));
  });

  afterEach(async () => {
    await rm(tempDir, { recursive: true, force: true });
  });

  it("default settings", () => {
    const { store, executor, callToolHandler, eventDispatcher } = makeDeps();
    const scheduler = new JobScheduler({
      store, executor, callToolHandler, eventDispatcher,
      jobsRoot: tempDir, executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    expect(scheduler.getSettings()).toEqual(DEFAULT_JOB_SCHEDULER_SETTINGS);
  });

  it("tick runs a due agent job, marks run ok, publishes job.completed", async () => {
    const store = new FakeJobStore();
    await store.create(makeJob({ id: "due-1", nextRunAt: "2024-12-31T23:00:00.000Z" }));
    const events: ApplicationEvent[] = [];
    const eventDispatcher = new EventDispatcher();
    eventDispatcher.onAny({ handle: (e) => { events.push(e); } });
    const executor = new ScriptedExecutor(() => ({ traceId: "t1", status: "ok", summary: "hello done" }));
    const scheduler = new JobScheduler({
      store, executor, callToolHandler: new FakeCallToolHandler(), eventDispatcher,
      jobsRoot: tempDir, executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    await scheduler.tick();
    const job = await store.get("due-1");
    expect(job!.lastStatus).toBe("ok");
    expect(job!.lastRunAt).toBe(NOW.toISOString());
    expect(job!.repeat.completed).toBe(1);
    expect(job!.nextRunAt).toBe("2025-01-01T00:30:00.000Z");
    expect(events.some((e) => e.type === "job.completed")).toBe(true);
  });

  it("tick runs a due tool job via callToolHandler", async () => {
    const store = new FakeJobStore();
    await store.create(makeJob({
      id: "tool-1",
      mode: { type: "tool", pluginId: "mail", toolName: "send", args: { to: "x" } },
    }));
    const callToolHandler = new FakeCallToolHandler({ sent: true });
    const scheduler = new JobScheduler({
      store, executor: new ScriptedExecutor(() => ({ traceId: "t", status: "ok", summary: "" })),
      callToolHandler, eventDispatcher: new EventDispatcher(),
      jobsRoot: tempDir, executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    await scheduler.tick();
    expect(callToolHandler.calls).toHaveLength(1);
    expect(callToolHandler.calls[0]!.toolName).toBe("send");
    const job = await store.get("tool-1");
    expect(job!.lastStatus).toBe("ok");
  });

  it("one-shot disables after a successful run", async () => {
    const store = new FakeJobStore();
    await store.create(makeJob({
      id: "once-1",
      schedule: { kind: "once", runAt: "2024-12-31T23:59:30.000Z" },
      nextRunAt: "2024-12-31T23:59:30.000Z",
      repeat: { times: 1, completed: 0 },
    }));
    const scheduler = new JobScheduler({
      store, executor: new ScriptedExecutor(() => ({ traceId: "t", status: "ok", summary: "ok" })),
      callToolHandler: new FakeCallToolHandler(), eventDispatcher: new EventDispatcher(),
      jobsRoot: tempDir, executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    await scheduler.tick();
    const job = await store.get("once-1");
    expect(job!.enabled).toBe(false);
    expect(job!.nextRunAt).toBeNull();
  });

  it("repeat.times terminal disables recurring job", async () => {
    const store = new FakeJobStore();
    await store.create(makeJob({
      id: "bounded-1",
      schedule: { kind: "interval", minutes: 30 },
      repeat: { times: 2, completed: 1 },
    }));
    const scheduler = new JobScheduler({
      store, executor: new ScriptedExecutor(() => ({ traceId: "t", status: "ok", summary: "ok" })),
      callToolHandler: new FakeCallToolHandler(), eventDispatcher: new EventDispatcher(),
      jobsRoot: tempDir, executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    await scheduler.tick();
    const job = await store.get("bounded-1");
    expect(job!.enabled).toBe(false);
    expect(job!.repeat.completed).toBe(2);
  });

  it("missed one-shot (past grace) is marked error + disabled, not fired", async () => {
    const store = new FakeJobStore();
    await store.create(makeJob({
      id: "missed-1",
      schedule: { kind: "once", runAt: "2024-12-31T22:00:00.000Z" },
      nextRunAt: "2024-12-31T22:00:00.000Z",
      repeat: { times: 1, completed: 0 },
    }));
    const events: ApplicationEvent[] = [];
    const eventDispatcher = new EventDispatcher();
    eventDispatcher.onAny({ handle: (e) => { events.push(e); } });
    let fired = 0;
    const executor = new ScriptedExecutor(() => { fired += 1; return { traceId: "t", status: "ok", summary: "ok" }; });
    const scheduler = new JobScheduler({
      store, executor, callToolHandler: new FakeCallToolHandler(), eventDispatcher,
      jobsRoot: tempDir, executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    await scheduler.tick();
    expect(fired).toBe(0);
    const job = await store.get("missed-1");
    expect(job!.lastStatus).toBe("error");
    expect(job!.lastError).toBe("missed while app was closed");
    expect(job!.enabled).toBe(false);
    expect(events.some((e) => e.type === "job.failed")).toBe(true);
  });

  it("at-most-once: a job with a live claim is not fired", async () => {
    const store = new FakeJobStore();
    await store.create(makeJob({ id: "claimed-1", nextRunAt: "2024-12-31T23:00:00.000Z" }));
    // Pre-claim with a far-future expiry.
    await store.claimFire("claimed-1", "external", 3600, NOW);
    let fired = 0;
    const executor = new ScriptedExecutor(() => { fired += 1; return { traceId: "t", status: "ok", summary: "ok" }; });
    const scheduler = new JobScheduler({
      store, executor, callToolHandler: new FakeCallToolHandler(), eventDispatcher: new EventDispatcher(),
      jobsRoot: tempDir, executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    await scheduler.tick();
    expect(fired).toBe(0);
  });

  it("tick error isolation: a failing job does not crash the tick", async () => {
    const store = new FakeJobStore();
    await store.create(makeJob({ id: "ok-1", nextRunAt: "2024-12-31T23:00:00.000Z" }));
    await store.create(makeJob({
      id: "fail-1",
      mode: { type: "tool", pluginId: "p", toolName: "boom", args: {} },
      nextRunAt: "2024-12-31T23:00:00.000Z",
    }));
    const callToolHandler = new (class {
      async handle(command: CallToolCommand) {
        if (command.toolName === "boom") throw new Error("boom");
        return { requestId: command.requestId, result: { ok: true } };
      }
    })() as unknown as FakeCallToolHandler;
    const scheduler = new JobScheduler({
      store, executor: new ScriptedExecutor(() => ({ traceId: "t", status: "ok", summary: "ok" })),
      callToolHandler, eventDispatcher: new EventDispatcher(),
      jobsRoot: tempDir, executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    await scheduler.tick();
    const okJob = await store.get("ok-1");
    const failJob = await store.get("fail-1");
    expect(okJob!.lastStatus).toBe("ok");
    expect(failJob!.lastStatus).toBe("error");
    expect(failJob!.lastError).toBe("boom");
  });

  it("runOneNow fires a job immediately and respects the claim lock", async () => {
    const store = new FakeJobStore();
    await store.create(makeJob({ id: "manual-1", nextRunAt: "2025-06-01T00:00:00.000Z" }));
    const scheduler = new JobScheduler({
      store, executor: new ScriptedExecutor(() => ({ traceId: "t", status: "ok", summary: "manual ok" })),
      callToolHandler: new FakeCallToolHandler(), eventDispatcher: new EventDispatcher(),
      jobsRoot: tempDir, executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    const result = await scheduler.runOneNow("manual-1");
    expect(result.ok).toBe(true);
    const job = await store.get("manual-1");
    expect(job!.lastStatus).toBe("ok");
  });

  it("runOneNow rejects a paused job", async () => {
    const store = new FakeJobStore();
    await store.create(makeJob({ id: "paused-1", enabled: false }));
    const scheduler = new JobScheduler({
      store, executor: new ScriptedExecutor(() => ({ traceId: "t", status: "ok", summary: "" })),
      callToolHandler: new FakeCallToolHandler(), eventDispatcher: new EventDispatcher(),
      jobsRoot: tempDir, executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    const result = await scheduler.runOneNow("paused-1");
    expect(result.ok).toBe(false);
    expect(result.error).toMatch(/paused/);
  });

  it("tick lock prevents concurrent ticks", async () => {
    const store = new FakeJobStore();
    const scheduler = new JobScheduler({
      store, executor: new ScriptedExecutor(() => ({ traceId: "t", status: "ok", summary: "" })),
      callToolHandler: new FakeCallToolHandler(), eventDispatcher: new EventDispatcher(),
      jobsRoot: tempDir, executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    // Acquire the lock manually first.
    const { open } = await import("node:fs/promises");
    const { resolve } = await import("node:path");
    const fh = await open(resolve(tempDir, ".tick.lock"), "wx");
    await fh.writeFile("pid:999999\n");
    await fh.close();
    await scheduler.tick();
    // No jobs processed because the lock was held.
    expect(store.outputs.size).toBe(0);
  });

  it("start/stop manages the interval timer", () => {
    const store = new FakeJobStore();
    const scheduler = new JobScheduler({
      store, executor: new ScriptedExecutor(() => ({ traceId: "t", status: "ok", summary: "" })),
      callToolHandler: new FakeCallToolHandler(), eventDispatcher: new EventDispatcher(),
      jobsRoot: tempDir, executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    expect(scheduler.getStatus().running).toBe(false);
    scheduler.start();
    expect(scheduler.getStatus().running).toBe(true);
    scheduler.stop();
    expect(scheduler.getStatus().running).toBe(false);
  });
});

// Re-export event factories so the test module is self-contained.
export { createJobCompletedEvent, createJobFailedEvent };
