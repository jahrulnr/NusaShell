import { describe, expect, it, beforeEach, afterEach, vi } from "vitest";
import { EventJobMatcher, MAX_CHAIN_DEPTH } from "../src/job/services/event-job-matcher.js";
import { EventDispatcher } from "../src/events/event-dispatcher.js";
import { createAutomationEvent } from "../src/events/automation-event.js";
import type { Job, JobOutputEntry, JobStorePort, JobTrigger } from "../src/job/job-model.js";

function makeEventJob(
  pattern: string,
  overrides: Partial<Job> = {},
): Job {
  return {
    id: `job-${pattern}-${Math.random().toString(36).slice(2, 8)}`,
    name: `Event job ${pattern}`,
    trigger: { kind: "event", pattern } as JobTrigger,
    mode: { type: "agent", prompt: "Do something" },
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

class FakeJobStore implements JobStorePort {
  jobs = new Map<string, Job>();
  claims = new Map<string, string>();
  outputs = new Map<string, JobOutputEntry[]>();

  async create(job: Job): Promise<Job> { this.jobs.set(job.id, job); return job; }
  async update(job: Job): Promise<Job> { this.jobs.set(job.id, job); return job; }
  async get(id: string): Promise<Job | null> { return this.jobs.get(id) ?? null; }
  async list(): Promise<readonly Job[]> { return [...this.jobs.values()]; }
  async remove(id: string): Promise<void> { this.jobs.delete(id); }
  async markRun(id: string, status: "ok" | "error", error: string | null, nextRunAt: string | null, now: Date): Promise<Job | null> {
    const existing = this.jobs.get(id);
    if (!existing) return null;
    const updated: Job = { ...existing, nextRunAt, lastRunAt: now.toISOString(), lastStatus: status, lastError: error };
    this.jobs.set(id, updated);
    return updated;
  }
  async claimFire(jobId: string, claimId: string): Promise<boolean> {
    if (this.claims.has(jobId)) return false;
    this.claims.set(jobId, claimId);
    return true;
  }
  async releaseFire(jobId: string): Promise<void> { this.claims.delete(jobId); }
  async listDue(): Promise<readonly Job[]> { return []; }
  async appendOutput(jobId: string, entry: JobOutputEntry): Promise<void> {
    const existing = this.outputs.get(jobId) ?? [];
    this.outputs.set(jobId, [entry, ...existing]);
  }
  async listOutputs(): Promise<readonly JobOutputEntry[]> { return []; }
}

describe("EventJobMatcher — Phase D cycle guard", () => {
  let store: FakeJobStore;
  let dispatcher: EventDispatcher;
  let startJobNowMock: ReturnType<typeof vi.fn>;
  let matcher: EventJobMatcher;

  beforeEach(() => {
    store = new FakeJobStore();
    dispatcher = new EventDispatcher();
    startJobNowMock = vi.fn().mockResolvedValue({ ok: true });
    matcher = new EventJobMatcher({
      store,
      scheduler: { startJobNow: startJobNowMock } as never,
      eventDispatcher: dispatcher,
    });
    matcher.start();
  });

  afterEach(() => {
    matcher.stop();
  });

  it("blocks self-trigger cycle (job emits event matching its own pattern)", async () => {
    const job = makeEventJob("job.done", {
      onComplete: { type: "job.done" },
    });
    store.jobs.set(job.id, job);
    // Simulate the job's own onComplete emission
    const selfEvent = createAutomationEvent(
      "job.done",
      undefined,
      {},
      new Date(),
      "evt-self",
      { jobId: job.id, chainDepth: 1 },
    );
    await dispatcher.publish(selfEvent);
    // The job should NOT fire on its own emission
    expect(startJobNowMock).not.toHaveBeenCalledWith(job.id, expect.any(Object), expect.any(Object));
  });

  it("allows chain: job A emits → job B fires", async () => {
    const jobA = makeEventJob("trigger.start", {
      onComplete: { type: "chain.next" },
    });
    const jobB = makeEventJob("chain.next");
    store.jobs.set(jobA.id, jobA);
    store.jobs.set(jobB.id, jobB);
    // Job A completes and emits chain.next
    const chainEvent = createAutomationEvent(
      "chain.next",
      undefined,
      {},
      new Date(),
      "evt-chain",
      { jobId: jobA.id, chainDepth: 1 },
    );
    await dispatcher.publish(chainEvent);
    // Job B should fire (it's a different job)
    expect(startJobNowMock).toHaveBeenCalledWith(jobB.id, expect.any(Object), expect.any(Object));
  });

  it("blocks chain exceeding MAX_CHAIN_DEPTH", async () => {
    const job = makeEventJob("deep.chain");
    store.jobs.set(job.id, job);
    // Emit an event at max chain depth
    const deepEvent = createAutomationEvent(
      "deep.chain",
      undefined,
      {},
      new Date(),
      "evt-deep",
      { jobId: "other-job", chainDepth: MAX_CHAIN_DEPTH },
    );
    await dispatcher.publish(deepEvent);
    expect(startJobNowMock).not.toHaveBeenCalled();
  });

  it("allows plugin-emitted events (no origin) to fire event-jobs", async () => {
    const job = makeEventJob("mail.new");
    store.jobs.set(job.id, job);
    await dispatcher.publish(createAutomationEvent("mail.new", "mail", { subject: "Hi" }));
    expect(startJobNowMock).toHaveBeenCalledWith(job.id, expect.any(Object), undefined);
  });

  it("passes chainOrigin to runOneNow for chained events", async () => {
    const jobB = makeEventJob("chain.next");
    store.jobs.set(jobB.id, jobB);
    const chainEvent = createAutomationEvent(
      "chain.next",
      undefined,
      {},
      new Date(),
      "evt-chain-2",
      { jobId: "job-a", chainDepth: 2 },
    );
    await dispatcher.publish(chainEvent);
    expect(startJobNowMock).toHaveBeenCalledWith(
      jobB.id,
      expect.any(Object),
      { originJobId: "job-a", chainDepth: 2 },
    );
  });
});
