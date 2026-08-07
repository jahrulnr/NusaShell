import { describe, expect, it, beforeEach, afterEach, vi } from "vitest";
import { mkdtemp, mkdir, open, readdir, readFile, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { tmpdir } from "node:os";
import {
  JobScheduler,
  DEFAULT_JOB_SCHEDULER_SETTINGS,
  DEFAULT_JOB_EXECUTOR_SETTINGS,
  type Job,
  type JobStorePort,
  type JobFsPort,
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
    trigger: { kind: "schedule", schedule: { kind: "interval", minutes: 30 } },
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
    const existingSchedule = existing.trigger.kind === "schedule" ? existing.trigger.schedule : null;
    const enabled = existingSchedule?.kind === "once" ? false : limitReached ? false : existing.enabled;
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
  readonly capturedOptions: { providerId?: string; model?: string; effort?: string }[] = [];
  constructor(private readonly respond: (prompt: string) => JobExecutionResult) {}
  async runAgent(
    prompt: string,
    _settings: JobAgentExecutorSettings,
    _signal?: AbortSignal,
    options?: { providerId?: string; model?: string; effort?: string },
  ): Promise<JobExecutionResult> {
    if (options) this.capturedOptions.push(options);
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

const TICK_LOCK_FILE = ".tick.lock";
const STALE_PID_AFTER_MS = 5 * 60 * 1000;

/** Test-only JobFsPort backed by the real filesystem (temp dir). */
class TestJobFs implements JobFsPort {
  constructor(private readonly root: string) {}

  async persistJobOutput(jobId: string, stamp: string, content: string): Promise<string | null> {
    try {
      const dir = resolve(this.root, "output", jobId);
      await mkdir(dir, { recursive: true });
      const path = resolve(dir, `${stamp}.md`);
      await writeFile(path, content, "utf8");
      return path;
    } catch {
      return null;
    }
  }

  async readJobOutput(path: string): Promise<string | null> {
    try {
      return await readFile(path, "utf8");
    } catch {
      return null;
    }
  }

  async acquireTickLock(): Promise<boolean> {
    const lockPath = resolve(this.root, TICK_LOCK_FILE);
    try {
      await mkdir(this.root, { recursive: true });
      try {
        const entries = await readdir(this.root);
        if (entries.includes(TICK_LOCK_FILE)) {
          const content = await readFile(lockPath, "utf8").catch(() => "");
          const pidMatch = /pid:(\d+)/.exec(content);
          const startedMatch = /started:(\S+)/.exec(content);
          const staleByAge = startedMatch
            ? Date.now() - new Date(startedMatch[1]!).getTime() > STALE_PID_AFTER_MS
            : false;
          const pidDead = pidMatch ? !processAlive(parseInt(pidMatch[1]!, 10)) : false;
          if (staleByAge || pidDead) {
            await rm(lockPath, { force: true });
          }
        }
      } catch {
        // best-effort
      }
      const fh = await open(lockPath, "wx");
      await fh.writeFile(`pid:${process.pid}\nstarted:${new Date().toISOString()}\n`);
      await fh.close();
      return true;
    } catch {
      return false;
    }
  }

  async releaseTickLock(): Promise<void> {
    await rm(resolve(this.root, TICK_LOCK_FILE), { force: true }).catch(() => {});
  }
}

function processAlive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

function makeDeps(overrides: Partial<{
  store: FakeJobStore;
  executor: ScriptedExecutor;
  callToolHandler: FakeCallToolHandler;
  eventDispatcher: EventDispatcher;
  jobFs: JobFsPort;
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
      jobFs: new TestJobFs(tempDir), executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
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
      jobFs: new TestJobFs(tempDir), executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
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
      jobFs: new TestJobFs(tempDir), executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
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
      trigger: { kind: "schedule", schedule: { kind: "once", runAt: "2024-12-31T23:59:30.000Z" } },
      nextRunAt: "2024-12-31T23:59:30.000Z",
      repeat: { times: 1, completed: 0 },
    }));
    const scheduler = new JobScheduler({
      store, executor: new ScriptedExecutor(() => ({ traceId: "t", status: "ok", summary: "ok" })),
      callToolHandler: new FakeCallToolHandler(), eventDispatcher: new EventDispatcher(),
      jobFs: new TestJobFs(tempDir), executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
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
      trigger: { kind: "schedule", schedule: { kind: "interval", minutes: 30 } },
      repeat: { times: 2, completed: 1 },
    }));
    const scheduler = new JobScheduler({
      store, executor: new ScriptedExecutor(() => ({ traceId: "t", status: "ok", summary: "ok" })),
      callToolHandler: new FakeCallToolHandler(), eventDispatcher: new EventDispatcher(),
      jobFs: new TestJobFs(tempDir), executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
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
      trigger: { kind: "schedule", schedule: { kind: "once", runAt: "2024-12-31T22:00:00.000Z" } },
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
      jobFs: new TestJobFs(tempDir), executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
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
      jobFs: new TestJobFs(tempDir), executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
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
      jobFs: new TestJobFs(tempDir), executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
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
      jobFs: new TestJobFs(tempDir), executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    const result = await scheduler.runOneNow("manual-1");
    expect(result.ok).toBe(true);
    const job = await store.get("manual-1");
    expect(job!.lastStatus).toBe("ok");
  });

  it("startJobNow returns before the job finishes and runs in background", async () => {
    const store = new FakeJobStore();
    await store.create(makeJob({ id: "bg-1", nextRunAt: "2025-06-01T00:00:00.000Z" }));
    let release!: () => void;
    const barrier = new Promise<void>((resolve) => { release = resolve; });
    const executor = {
      runAgent: async () => {
        await barrier;
        return { traceId: "t", status: "ok", summary: "bg ok" };
      },
    };
    const scheduler = new JobScheduler({
      store,
      executor: executor as never,
      callToolHandler: new FakeCallToolHandler(),
      eventDispatcher: new EventDispatcher(),
      jobFs: new TestJobFs(tempDir),
      executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    const result = await scheduler.startJobNow("bg-1");
    expect(result.ok).toBe(true);
    // Returns before the job completes.
    const jobAfterLaunch = await store.get("bg-1");
    expect(jobAfterLaunch!.lastStatus).toBeNull();
    release();
    await vi.waitFor(() => expect(store.get("bg-1").then((j) => j!.lastStatus)).resolves.toBe("ok"));
  });

  it("startJobNow rejects a paused job", async () => {
    const store = new FakeJobStore();
    await store.create(makeJob({ id: "bg-paused", enabled: false }));
    const scheduler = new JobScheduler({
      store, executor: new ScriptedExecutor(() => ({ traceId: "t", status: "ok", summary: "" })),
      callToolHandler: new FakeCallToolHandler(), eventDispatcher: new EventDispatcher(),
      jobFs: new TestJobFs(tempDir), executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    const result = await scheduler.startJobNow("bg-paused");
    expect(result.ok).toBe(false);
    expect(result.error).toMatch(/paused/);
  });

  it("releases the fire claim when dispatch throws mid-run", async () => {
    const store = new FakeJobStore();
    await store.create(makeJob({ id: "throwing-1", nextRunAt: "2025-06-01T00:00:00.000Z" }));
    const executor = {
      runAgent: async () => {
        throw new Error("boom");
      },
    };
    const scheduler = new JobScheduler({
      store,
      executor: executor as never,
      callToolHandler: new FakeCallToolHandler(),
      eventDispatcher: new EventDispatcher(),
      jobFs: new TestJobFs(tempDir),
      executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    await scheduler.startJobNow("throwing-1");
    // Let the background dispatch settle and its finally run.
    await vi.waitFor(() => expect(store.claims.has("throwing-1")).toBe(false));
  });

  it("runOneNow rejects a paused job", async () => {
    const store = new FakeJobStore();
    await store.create(makeJob({ id: "paused-1", enabled: false }));
    const scheduler = new JobScheduler({
      store, executor: new ScriptedExecutor(() => ({ traceId: "t", status: "ok", summary: "" })),
      callToolHandler: new FakeCallToolHandler(), eventDispatcher: new EventDispatcher(),
      jobFs: new TestJobFs(tempDir), executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
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
      jobFs: new TestJobFs(tempDir), executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
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
      jobFs: new TestJobFs(tempDir), executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    expect(scheduler.getStatus().running).toBe(false);
    scheduler.start();
    expect(scheduler.getStatus().running).toBe(true);
    scheduler.stop();
    expect(scheduler.getStatus().running).toBe(false);
  });

  it("publishes job.started when dispatch begins", async () => {
    const store = new FakeJobStore();
    await store.create(makeJob({ id: "start-1", nextRunAt: "2024-12-31T23:00:00.000Z" }));
    const events: ApplicationEvent[] = [];
    const eventDispatcher = new EventDispatcher();
    eventDispatcher.onAny({ handle: (e) => { events.push(e); } });
    const scheduler = new JobScheduler({
      store, executor: new ScriptedExecutor(() => ({ traceId: "t-start", status: "ok", summary: "done" })),
      callToolHandler: new FakeCallToolHandler(), eventDispatcher,
      jobFs: new TestJobFs(tempDir), executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    await scheduler.tick();
    const started = events.find((e) => e.type === "job.started");
    expect(started).toBeDefined();
    expect((started as unknown as { jobId: string }).jobId).toBe("start-1");
  });

  it("cancel aborts an in-flight run and marks cancelled", async () => {
    const store = new FakeJobStore();
    await store.create(makeJob({ id: "cancel-1", nextRunAt: "2024-12-31T23:00:00.000Z" }));
    const events: ApplicationEvent[] = [];
    const eventDispatcher = new EventDispatcher();
    eventDispatcher.onAny({ handle: (e) => { events.push(e); } });

    // Executor that blocks until the signal aborts, then returns an error.
    const executor = {
      async runAgent(_prompt: string, _settings: JobAgentExecutorSettings, signal?: AbortSignal): Promise<JobExecutionResult> {
        if (!signal) return { traceId: "t-cancel", status: "ok", summary: "no signal" };
        return new Promise((resolve) => {
          if (signal.aborted) {
            resolve({ traceId: "t-cancel", status: "error", summary: "aborted", error: "aborted" });
            return;
          }
          signal.addEventListener("abort", () => {
            resolve({ traceId: "t-cancel", status: "error", summary: "aborted", error: "aborted" });
          }, { once: true });
        });
      },
    };

    const scheduler = new JobScheduler({
      store, executor, callToolHandler: new FakeCallToolHandler(), eventDispatcher,
      jobFs: new TestJobFs(tempDir), executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });

    // Start the tick (dispatch runs async), then cancel while in-flight.
    const tickPromise = scheduler.tick();
    // Wait for the job to be registered as active by polling.
    while (!scheduler.isRunning("cancel-1")) {
      await new Promise((r) => setTimeout(r, 1));
    }
    expect(scheduler.isRunning("cancel-1")).toBe(true);
    const cancelResult = await scheduler.cancel("cancel-1");
    expect(cancelResult.ok).toBe(true);
    await tickPromise;

    const job = await store.get("cancel-1");
    expect(job!.lastStatus).toBe("cancelled");
    expect(events.some((e) => e.type === "job.cancelled")).toBe(true);
    expect(scheduler.isRunning("cancel-1")).toBe(false);
  });

  it("cancel returns not-running for an idle job", async () => {
    const store = new FakeJobStore();
    await store.create(makeJob({ id: "idle-1" }));
    const scheduler = new JobScheduler({
      store, executor: new ScriptedExecutor(() => ({ traceId: "t", status: "ok", summary: "" })),
      callToolHandler: new FakeCallToolHandler(), eventDispatcher: new EventDispatcher(),
      jobFs: new TestJobFs(tempDir), executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    const result = await scheduler.cancel("idle-1");
    expect(result.ok).toBe(false);
    expect(result.error).toMatch(/not running/);
  });

  it("job.output entries include traceId", async () => {
    const store = new FakeJobStore();
    await store.create(makeJob({ id: "trace-1", nextRunAt: "2024-12-31T23:00:00.000Z" }));
    const scheduler = new JobScheduler({
      store, executor: new ScriptedExecutor(() => ({ traceId: "trace-xyz", status: "ok", summary: "done" })),
      callToolHandler: new FakeCallToolHandler(), eventDispatcher: new EventDispatcher(),
      jobFs: new TestJobFs(tempDir), executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    await scheduler.tick();
    const outputs = await store.listOutputs("trace-1", 10);
    expect(outputs.length).toBe(1);
    expect(outputs[0]!.traceId).toBeDefined();
  });

  it("passes the job's agent-mode provider/model/effort to the executor", async () => {
    const store = new FakeJobStore();
    await store.create(makeJob({
      id: "model-job",
      nextRunAt: "2024-12-31T23:00:00.000Z",
      mode: { type: "agent", prompt: "Say hello", providerId: "anthropic", model: "claude-3-5-sonnet", effort: "high" },
    }));
    const executor = new ScriptedExecutor(() => ({ traceId: "t", status: "ok", summary: "ok" }));
    const scheduler = new JobScheduler({
      store, executor,
      callToolHandler: new FakeCallToolHandler(), eventDispatcher: new EventDispatcher(),
      jobFs: new TestJobFs(tempDir), executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    await scheduler.tick();
    expect(executor.capturedOptions).toEqual([{ providerId: "anthropic", model: "claude-3-5-sonnet", effort: "high" }]);
  });

  it("omits model options when the agent-mode job does not set them", async () => {
    const store = new FakeJobStore();
    await store.create(makeJob({ id: "plain-job", nextRunAt: "2024-12-31T23:00:00.000Z" }));
    const executor = new ScriptedExecutor(() => ({ traceId: "t", status: "ok", summary: "ok" }));
    const scheduler = new JobScheduler({
      store, executor,
      callToolHandler: new FakeCallToolHandler(), eventDispatcher: new EventDispatcher(),
      jobFs: new TestJobFs(tempDir), executorSettings: DEFAULT_JOB_EXECUTOR_SETTINGS as unknown as JobAgentExecutorSettings,
      now: () => NOW,
    });
    await scheduler.tick();
    expect(executor.capturedOptions).toEqual([{}]);
  });
});

// Re-export event factories so the test module is self-contained.
export { createJobCompletedEvent, createJobFailedEvent };
