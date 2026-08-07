import { describe, expect, it, beforeEach, afterEach, vi } from "vitest";
import {
  EventJobMatcher,
  matchGlob,
  evaluateCondition,
  resolveDotPath,
  normalizeTrigger,
  EventDispatcher,
  createAutomationEvent,
  type Job,
  type JobStorePort,
  type JobOutputEntry,
  type JobSchedule,
  type Pipeline,
  type PipelineRun,
  type PipelineStorePort,
} from "../src/index.js";

function makeEventJob(
  pattern: string,
  overrides: Partial<Job> = {},
): Job {
  return {
    id: "job-evt-1",
    name: "Event job",
    trigger: { kind: "event", pattern },
    mode: { type: "agent", prompt: "Handle event" },
    enabled: true,
    repeat: { times: null, completed: 0 },
    nextRunAt: null,
    lastRunAt: null,
    lastStatus: null,
    lastError: null,
    createdAt: "2025-01-01T00:00:00.000Z",
    ...overrides,
  };
}

function makeScheduleJob(overrides: Partial<Job> = {}): Job {
  return {
    id: "job-sched-1",
    name: "Schedule job",
    trigger: { kind: "schedule", schedule: { kind: "interval", minutes: 30 } },
    mode: { type: "agent", prompt: "Hello" },
    enabled: true,
    repeat: { times: null, completed: 0 },
    nextRunAt: "2025-01-01T00:30:00.000Z",
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

  async create(job: Job): Promise<Job> { this.jobs.set(job.id, job); return job; }
  async update(job: Job): Promise<Job> { this.jobs.set(job.id, job); return job; }
  async get(id: string): Promise<Job | null> { return this.jobs.get(id) ?? null; }
  async list(): Promise<readonly Job[]> { return [...this.jobs.values()]; }
  async remove(id: string): Promise<void> { this.jobs.delete(id); this.claims.delete(id); this.outputs.delete(id); }
  async markRun(id: string, status: "ok" | "error", error: string | null, nextRunAt: string | null, now: Date): Promise<Job | null> {
    const existing = this.jobs.get(id);
    if (!existing) return null;
    const updated: Job = { ...existing, nextRunAt, lastRunAt: now.toISOString(), lastStatus: status, lastError: error };
    this.jobs.set(id, updated);
    return updated;
  }
  async claimFire(jobId: string, claimId: string, _ttl: number, _now: Date): Promise<boolean> {
    if (this.claims.has(jobId)) return false;
    this.claims.set(jobId, claimId);
    return true;
  }
  async releaseFire(jobId: string): Promise<void> { this.claims.delete(jobId); }
  async listDue(_now: Date): Promise<readonly Job[]> { return []; }
  async appendOutput(jobId: string, entry: JobOutputEntry): Promise<void> {
    const existing = this.outputs.get(jobId) ?? [];
    this.outputs.set(jobId, [entry, ...existing]);
  }
  async listOutputs(jobId: string, limit: number): Promise<readonly JobOutputEntry[]> {
    return (this.outputs.get(jobId) ?? []).slice(0, limit);
  }
}

class FakePipelineStore implements PipelineStorePort {
  pipelines = new Map<string, Pipeline>();

  async create(p: Pipeline): Promise<Pipeline> { this.pipelines.set(p.id, p); return p; }
  async update(p: Pipeline): Promise<Pipeline> { this.pipelines.set(p.id, p); return p; }
  async get(id: string): Promise<Pipeline | null> { return this.pipelines.get(id) ?? null; }
  async list(): Promise<readonly Pipeline[]> { return [...this.pipelines.values()]; }
  async remove(id: string): Promise<void> { this.pipelines.delete(id); }
  async claimRun(): Promise<PipelineRun | null> { return null; }
  async updateRun(run: PipelineRun): Promise<PipelineRun | null> { return run; }
  async finalizeRun(run: PipelineRun): Promise<PipelineRun | null> { return run; }
  async getRun(): Promise<PipelineRun | null> { return null; }
  async getActiveRun(): Promise<PipelineRun | null> { return null; }
  async listRuns(): Promise<readonly PipelineRun[]> { return []; }
  async listDueSchedules(): Promise<readonly Pipeline[]> { return []; }
  async recoverExpiredLeases(): Promise<number> { return 0; }
  async markRun(id: string, status: "ok" | "error" | "cancelled", error: string | null, now: Date): Promise<Pipeline | null> {
    const existing = this.pipelines.get(id);
    if (!existing) return null;
    const updated: Pipeline = { ...existing, lastRunAt: now.toISOString(), lastStatus: status, lastError: error };
    this.pipelines.set(id, updated);
    return updated;
  }
}

describe("matchGlob", () => {
  it("matches exact patterns", () => {
    expect(matchGlob("mail.new", "mail.new")).toBe(true);
    expect(matchGlob("mail.new", "mail.old")).toBe(false);
  });

  it("matches single-segment wildcards", () => {
    expect(matchGlob("mail.*", "mail.new")).toBe(true);
    expect(matchGlob("mail.*", "mail.sent")).toBe(true);
    expect(matchGlob("mail.*", "resource.updated")).toBe(false);
  });

  it("matches multi-segment wildcards", () => {
    expect(matchGlob("**.updated", "resource.updated")).toBe(true);
    expect(matchGlob("**.updated", "mail.folder.updated")).toBe(true);
    expect(matchGlob("**.updated", "mail.new")).toBe(false);
  });

  it("does not match across segments with single *", () => {
    expect(matchGlob("mail.*", "mail.folder.new")).toBe(false);
  });
});

describe("resolveDotPath", () => {
  it("resolves nested paths", () => {
    const obj = { payload: { subject: "Hello", from: { address: "a@b.com" } } };
    expect(resolveDotPath(obj, "payload.subject")).toBe("Hello");
    expect(resolveDotPath(obj, "payload.from.address")).toBe("a@b.com");
  });

  it("returns undefined for missing paths", () => {
    expect(resolveDotPath({ a: 1 }, "b.c")).toBe(undefined);
    expect(resolveDotPath(null, "a")).toBe(undefined);
  });

  it("stringifies non-string values", () => {
    expect(String(resolveDotPath({ count: 3 }, "count"))).toBe("3");
  });
});

describe("evaluateCondition", () => {
  const event = createAutomationEvent("mail.new", "mail", { subject: "Re: Hello", priority: "high", count: 5 });

  it("eq matches exact string", () => {
    expect(evaluateCondition({ path: "payload.priority", op: "eq", value: "high" }, event)).toBe(true);
    expect(evaluateCondition({ path: "payload.priority", op: "eq", value: "low" }, event)).toBe(false);
  });

  it("contains matches substring", () => {
    expect(evaluateCondition({ path: "payload.subject", op: "contains", value: "Re:" }, event)).toBe(true);
    expect(evaluateCondition({ path: "payload.subject", op: "contains", value: "Fwd:" }, event)).toBe(false);
  });

  it("regex matches pattern", () => {
    expect(evaluateCondition({ path: "payload.subject", op: "regex", value: "^Re:" }, event)).toBe(true);
    expect(evaluateCondition({ path: "payload.subject", op: "regex", value: "^Fwd:" }, event)).toBe(false);
  });

  it("returns false for missing path", () => {
    expect(evaluateCondition({ path: "payload.nonexistent", op: "eq", value: "x" }, event)).toBe(false);
  });
});

describe("EventJobMatcher", () => {
  let store: FakeJobStore;
  let dispatcher: EventDispatcher;
  let startJobNowMock: ReturnType<typeof vi.fn>;
  let matcher: EventJobMatcher;
  let nowMs: number;

  beforeEach(() => {
    store = new FakeJobStore();
    dispatcher = new EventDispatcher();
    startJobNowMock = vi.fn().mockResolvedValue({ ok: true });
    nowMs = Date.now();
    matcher = new EventJobMatcher({
      store,
      scheduler: { startJobNow: startJobNowMock } as never,
      eventDispatcher: dispatcher,
      now: () => new Date(nowMs),
    });
    matcher.start();
  });

  afterEach(() => {
    matcher.stop();
  });

  it("fires a matching event-job", async () => {
    const job = makeEventJob("mail.new");
    store.jobs.set(job.id, job);
    await dispatcher.publish(createAutomationEvent("mail.new", "mail", { subject: "Hello" }));
    expect(startJobNowMock).toHaveBeenCalledWith(job.id, expect.any(Object), undefined);
  });

  it("does not fire a non-matching event-job", async () => {
    const job = makeEventJob("mail.new");
    store.jobs.set(job.id, job);
    await dispatcher.publish(createAutomationEvent("resource.updated", "files", { uri: "file:///x" }));
    expect(startJobNowMock).not.toHaveBeenCalled();
  });

  it("does not fire disabled event-jobs", async () => {
    const job = makeEventJob("mail.new", { enabled: false });
    store.jobs.set(job.id, job);
    await dispatcher.publish(createAutomationEvent("mail.new", "mail", {}));
    expect(startJobNowMock).not.toHaveBeenCalled();
  });

  it("does not fire schedule-trigger jobs", async () => {
    const job = makeScheduleJob();
    store.jobs.set(job.id, job);
    await dispatcher.publish(createAutomationEvent("mail.new", "mail", {}));
    expect(startJobNowMock).not.toHaveBeenCalled();
  });

  it("filters by pluginId when set", async () => {
    const job = makeEventJob("mail.new", { trigger: { kind: "event", pattern: "mail.new", pluginId: "mail" } });
    store.jobs.set(job.id, job);
    await dispatcher.publish(createAutomationEvent("mail.new", "other-plugin", {}));
    expect(startJobNowMock).not.toHaveBeenCalled();
    await dispatcher.publish(createAutomationEvent("mail.new", "mail", {}));
    expect(startJobNowMock).toHaveBeenCalledWith(job.id, expect.any(Object), undefined);
  });

  it("matches glob patterns", async () => {
    const job = makeEventJob("mail.*");
    store.jobs.set(job.id, job);
    await dispatcher.publish(createAutomationEvent("mail.sent", "mail", {}));
    expect(startJobNowMock).toHaveBeenCalledWith(job.id, expect.any(Object), undefined);
  });

  it("evaluates conditions (AND)", async () => {
    const job = makeEventJob("mail.new", {
      trigger: {
        kind: "event",
        pattern: "mail.new",
        conditions: [
          { path: "payload.priority", op: "eq", value: "high" },
          { path: "payload.subject", op: "contains", value: "Re:" },
        ],
      },
    });
    store.jobs.set(job.id, job);
    await dispatcher.publish(createAutomationEvent("mail.new", "mail", { priority: "low", subject: "Re: Hello" }));
    expect(startJobNowMock).not.toHaveBeenCalled();
    await dispatcher.publish(createAutomationEvent("mail.new", "mail", { priority: "high", subject: "Re: Hello" }));
    expect(startJobNowMock).toHaveBeenCalledWith(job.id, expect.any(Object), undefined);
  });

  it("respects maxFiresPerHour cap", async () => {
    const job = makeEventJob("mail.new", {
      trigger: { kind: "event", pattern: "mail.new", maxFiresPerHour: 2 },
    });
    store.jobs.set(job.id, job);
    for (let i = 0; i < 5; i++) {
      await dispatcher.publish(createAutomationEvent("mail.new", "mail", { i }));
    }
    expect(startJobNowMock).toHaveBeenCalledTimes(2);
  });

  it("coalesces events within throttleMs (latest wins)", async () => {
    vi.useFakeTimers();
    const job = makeEventJob("mail.new", {
      trigger: { kind: "event", pattern: "mail.new", throttleMs: 1000 },
    });
    store.jobs.set(job.id, job);
    await dispatcher.publish(createAutomationEvent("mail.new", "mail", { seq: 1 }));
    expect(startJobNowMock).toHaveBeenCalledTimes(1);
    // Within throttle window — should coalesce, not fire immediately
    await dispatcher.publish(createAutomationEvent("mail.new", "mail", { seq: 2 }));
    expect(startJobNowMock).toHaveBeenCalledTimes(1);
    // Advance past throttle window
    vi.advanceTimersByTime(1100);
    await vi.waitFor(() => expect(startJobNowMock).toHaveBeenCalledTimes(2));
    vi.useRealTimers();
  });
});

describe("EventJobMatcher.matchPipelines (Phase E)", () => {
  let pStore: FakePipelineStore;
  let dispatcher: EventDispatcher;
  let runPipelineMock: ReturnType<typeof vi.fn>;
  let pMatcher: EventJobMatcher;
  let pNowMs: number;
  const PIPELINE_A = "pipe-a-123";
  const PIPELINE_B = "pipe-b-123";

  function makePipeline(id: string, pattern: string, overrides: Partial<Pipeline> = {}): Pipeline {
    return {
      id,
      name: `Pipeline ${id}`,
      enabled: true,
      trigger: { kind: "event", pattern },
      steps: [],
      createdAt: "2025-01-01T00:00:00.000Z",
      nextRunAt: null,
      lastRunAt: null,
      lastStatus: null,
      lastError: null,
      ...overrides,
    };
  }

  beforeEach(() => {
    pStore = new FakePipelineStore();
    dispatcher = new EventDispatcher();
    runPipelineMock = vi.fn().mockResolvedValue({ ok: true });
    pNowMs = Date.now();
    pMatcher = new EventJobMatcher({
      store: new FakeJobStore(),
      scheduler: { startJobNow: vi.fn().mockResolvedValue({ ok: true }) } as never,
      eventDispatcher: dispatcher,
      now: () => new Date(pNowMs),
      pipelineStore: pStore,
      pipelineScheduler: { runPipeline: runPipelineMock } as never,
    });
    pMatcher.start();
  });

  afterEach(() => {
    pMatcher.stop();
    vi.useRealTimers();
  });

  it("fires an event-triggered pipeline with the event context", async () => {
    const pipeline = makePipeline(PIPELINE_A, "mail.new");
    pStore.pipelines.set(pipeline.id, pipeline);
    await dispatcher.publish(createAutomationEvent("mail.new", "mail", { subject: "Hi" }));
    await vi.waitFor(() => expect(runPipelineMock).toHaveBeenCalledTimes(1));
    expect(runPipelineMock).toHaveBeenCalledWith(
      PIPELINE_A,
      expect.objectContaining({ event: expect.objectContaining({ payload: { subject: "Hi" } }) }),
      { source: "event" },
    );
  });

  it("blocks self-trigger (event origin = this pipeline)", async () => {
    const pipeline = makePipeline(PIPELINE_A, "mail.new");
    pStore.pipelines.set(pipeline.id, pipeline);
    // Event originates from pipeline A ITSELF -> must not re-trigger A.
    await dispatcher.publish(
      createAutomationEvent("mail.new", "mail", { subject: "Hi" }, new Date(), "evt-self", {
        pipelineId: PIPELINE_A,
        chainDepth: 0,
      }),
    );
    await new Promise((r) => setTimeout(r, 20));
    expect(runPipelineMock).not.toHaveBeenCalled();
  });

  it("blocks a chain that crossed MAX_CHAIN_DEPTH (B at depth cap cannot trigger A)", async () => {
    const a = makePipeline(PIPELINE_A, "b.done");
    pStore.pipelines.set(a.id, a);
    // Origin pipeline B at depth 8 (= MAX_CHAIN_DEPTH) must not trigger A.
    await dispatcher.publish(
      createAutomationEvent("b.done", "nusashell", { p: 1 }, new Date(), "evt-deep", {
        pipelineId: PIPELINE_B,
        chainDepth: 8,
      }),
    );
    await new Promise((r) => setTimeout(r, 20));
    expect(runPipelineMock).not.toHaveBeenCalled();
  });

  it("coalesces pipeline events within throttleMs (latest wins, single fire)", async () => {
    vi.useFakeTimers();
    const pipeline = makePipeline(PIPELINE_A, "mail.new", {
      trigger: { kind: "event", pattern: "mail.new", throttleMs: 1000 },
    });
    pStore.pipelines.set(pipeline.id, pipeline);
    await dispatcher.publish(createAutomationEvent("mail.new", "mail", { seq: 1 }));
    expect(runPipelineMock).toHaveBeenCalledTimes(1);
    // Within throttle window -> coalesce, do not fire a second time yet.
    await dispatcher.publish(createAutomationEvent("mail.new", "mail", { seq: 2 }));
    expect(runPipelineMock).toHaveBeenCalledTimes(1);
    // Advance past window -> coalesced fire fires once with the latest payload.
    vi.advanceTimersByTime(1100);
    await vi.waitFor(() => expect(runPipelineMock).toHaveBeenCalledTimes(2));
    expect(runPipelineMock).toHaveBeenLastCalledWith(
      PIPELINE_A,
      expect.objectContaining({ event: expect.objectContaining({ payload: { seq: 2 } }) }),
      { source: "event" },
    );
  });
});

describe("normalizeTrigger (migration)", () => {
  it("wraps legacy schedule into trigger", () => {
    const schedule: JobSchedule = { kind: "interval", minutes: 30 };
    const trigger = normalizeTrigger({ schedule });
    expect(trigger).toEqual({ kind: "schedule", schedule });
  });

  it("passes through existing trigger", () => {
    const trigger = { kind: "event", pattern: "mail.new" } as const;
    expect(normalizeTrigger({ trigger })).toBe(trigger);
  });

  it("throws when neither is present", () => {
    expect(() => normalizeTrigger({})).toThrow(/missing both/);
  });
});
