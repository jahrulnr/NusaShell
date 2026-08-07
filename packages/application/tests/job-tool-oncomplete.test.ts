import { describe, expect, it, beforeEach, vi } from "vitest";
import { execJob } from "../src/agent/services/job-tool-handler.js";
import { type Job } from "../src/job/job-model.js";
import type { JobStorePort, JobOutputEntry } from "../src/job/index.js";

class FakeJobStore implements JobStorePort {
  jobs = new Map<string, Job>();
  outputs = new Map<string, JobOutputEntry[]>();

  async create(j: Job): Promise<Job> { this.jobs.set(j.id, j); return j; }
  async update(j: Job): Promise<Job> { this.jobs.set(j.id, j); return j; }
  async get(id: string): Promise<Job | null> { return this.jobs.get(id) ?? null; }
  async list(): Promise<readonly Job[]> { return [...this.jobs.values()]; }
  async remove(id: string): Promise<void> { this.jobs.delete(id); }
  async listOutputs(id: string, limit: number): Promise<readonly JobOutputEntry[]> {
    return (this.outputs.get(id) ?? []).slice(0, limit);
  }
  async appendOutput(_id: string, _entry: JobOutputEntry): Promise<void> {}
  async markRun(id: string, status: "ok" | "error" | "cancelled", error: string | null, nextRunAt: string | null): Promise<Job | null> {
    const job = this.jobs.get(id);
    if (!job) return null;
    const updated = { ...job, lastStatus: status, lastError: error, nextRunAt };
    this.jobs.set(id, updated);
    return updated;
  }
  async claimFire(id: string, _claimId: string, _ttlSeconds: number): Promise<boolean> {
    return this.jobs.has(id);
  }
  async releaseFire(_id: string, _claimId: string): Promise<void> {}
  async listDue(): Promise<readonly Job[]> { return [...this.jobs.values()]; }
}

describe("job tool handler — on_complete", () => {
  let store: FakeJobStore;
  let scheduler: { runOneNow: ReturnType<typeof vi.fn>; cancel: ReturnType<typeof vi.fn>; isRunning: ReturnType<typeof vi.fn>; activeTraceId: ReturnType<typeof vi.fn> };

  beforeEach(() => {
    store = new FakeJobStore();
    scheduler = {
      runOneNow: vi.fn().mockResolvedValue({ ok: true }),
      cancel: vi.fn().mockResolvedValue({ ok: true }),
      isRunning: vi.fn().mockReturnValue(false),
      activeTraceId: vi.fn().mockReturnValue(undefined),
    };
  });

  it("adds a job with on_complete", async () => {
    const result = await execJob(store, scheduler as never, {
      action: "add",
      name: "Chain starter",
      trigger: { kind: "event", pattern: "mail.new" },
      mode: "agent",
      prompt: "Classify email",
      on_complete: { type: "mail.classified", payload: { source: "auto" } },
    });
    expect(result).toHaveProperty("ok", true);
    const job = [...store.jobs.values()][0]!;
    expect(job.onComplete).toBeDefined();
    expect(job.onComplete!.type).toBe("mail.classified");
    expect(job.onComplete!.payload).toEqual({ source: "auto" });
  });

  it("adds a job without on_complete (omitted)", async () => {
    const result = await execJob(store, scheduler as never, {
      action: "add",
      name: "No chain",
      trigger: { kind: "event", pattern: "test" },
      mode: "agent",
      prompt: "test",
    });
    expect(result).toHaveProperty("ok", true);
    const job = [...store.jobs.values()][0]!;
    expect(job.onComplete).toBeUndefined();
  });

  it("updates a job with on_complete", async () => {
    const addResult = await execJob(store, scheduler as never, {
      action: "add",
      name: "Original",
      trigger: { kind: "event", pattern: "test" },
      mode: "agent",
      prompt: "test",
    });
    const id = (addResult as { data: Job }).data.id;
    const result = await execJob(store, scheduler as never, {
      action: "update",
      id,
      on_complete: { type: "test.done" },
    });
    expect(result).toHaveProperty("ok", true);
    expect(store.jobs.get(id)!.onComplete).toBeDefined();
    expect(store.jobs.get(id)!.onComplete!.type).toBe("test.done");
  });

  it("rejects invalid on_complete (missing type)", async () => {
    const result = await execJob(store, scheduler as never, {
      action: "add",
      name: "Bad chain",
      trigger: { kind: "event", pattern: "test" },
      mode: "agent",
      prompt: "test",
      on_complete: { payload: { x: 1 } },
    });
    expect(result).toHaveProperty("ok", false);
  });

  it("rejects invalid on_complete (payload not object)", async () => {
    const result = await execJob(store, scheduler as never, {
      action: "add",
      name: "Bad payload",
      trigger: { kind: "event", pattern: "test" },
      mode: "agent",
      prompt: "test",
      on_complete: { type: "test.done", payload: "not an object" },
    });
    expect(result).toHaveProperty("ok", false);
  });
});
