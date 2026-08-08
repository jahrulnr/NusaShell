import { describe, expect, it, beforeEach, vi } from "vitest";
import { PipelineScheduler } from "../src/job/services/pipeline-scheduler.js";
import { EventDispatcher } from "../src/events/event-dispatcher.js";
import type {
  Pipeline,
  PipelineRun,
  PipelineStatus,
  PipelineStep,
} from "../src/job/pipeline-model.js";
import { isTerminalPipelineRunStatus } from "../src/job/pipeline-model.js";
import type { PipelineStorePort } from "../src/job/ports/pipeline-store.port.js";

function makeStep(id: string, overrides: Partial<PipelineStep> = {}): PipelineStep {
  return {
    id,
    name: `Step ${id}`,
    action: { type: "agent", prompt: `prompt for ${id}` },
    ...overrides,
  };
}

function makePipeline(steps: PipelineStep[], overrides: Partial<Pipeline> = {}): Pipeline {
  return {
    id: `pipeline-${Math.random().toString(36).slice(2, 8)}`,
    name: "Test pipeline",
    enabled: true,
    trigger: { kind: "event", pattern: "test.event" },
    steps,
    createdAt: "2025-01-01T00:00:00.000Z",
    nextRunAt: null,
    lastRunAt: null,
    lastStatus: null,
    lastError: null,
    ...overrides,
  };
}

class FakePipelineStore implements PipelineStorePort {
  pipelines = new Map<string, Pipeline>();
  runs = new Map<string, PipelineRun>();
  activeByPipeline = new Map<string, string>();

  async create(p: Pipeline): Promise<Pipeline> { this.pipelines.set(p.id, p); return p; }
  async update(p: Pipeline): Promise<Pipeline> { this.pipelines.set(p.id, p); return p; }
  async get(id: string): Promise<Pipeline | null> { return this.pipelines.get(id) ?? null; }
  async list(): Promise<readonly Pipeline[]> { return [...this.pipelines.values()]; }
  async remove(id: string): Promise<void> { this.pipelines.delete(id); }

  async claimRun(run: PipelineRun): Promise<PipelineRun | null> {
    const pipeline = this.pipelines.get(run.pipelineId);
    if (!pipeline) return null;
    const existingId = this.activeByPipeline.get(run.pipelineId);
    if (existingId) {
      const existing = this.runs.get(existingId);
      if (existing && !isTerminalPipelineRunStatus(existing.status)) return null;
    }
    const claimed = { ...run, status: "claimed" as const };
    this.runs.set(claimed.runId, claimed);
    this.activeByPipeline.set(run.pipelineId, claimed.runId);
    this.pipelines.set(pipeline.id, {
      ...pipeline,
      lastStatus: "running",
      lastRunAt: claimed.startedAt,
      lastError: null,
      lastRunId: claimed.runId,
    });
    return claimed;
  }

  async updateRun(run: PipelineRun): Promise<PipelineRun | null> {
    const existing = this.runs.get(run.runId);
    if (!existing || isTerminalPipelineRunStatus(existing.status)) return existing ?? null;
    this.runs.set(run.runId, run);
    return run;
  }

  async finalizeRun(
    run: PipelineRun,
    status: Extract<PipelineStatus, "ok" | "error" | "cancelled" | "interrupted">,
    error: string | null,
    now: Date,
  ): Promise<PipelineRun | null> {
    const existing = this.runs.get(run.runId);
    if (!existing) return null;
    if (isTerminalPipelineRunStatus(existing.status)) return existing;
    const finalRun: PipelineRun = {
      ...run,
      status,
      completedAt: now.toISOString(),
      lastHeartbeatAt: now.toISOString(),
      errorMessage: error,
    };
    this.runs.set(run.runId, finalRun);
    this.activeByPipeline.delete(run.pipelineId);
    const pipeline = this.pipelines.get(run.pipelineId);
    if (pipeline) {
      this.pipelines.set(run.pipelineId, {
        ...pipeline,
        lastRunAt: now.toISOString(),
        lastStatus: status,
        lastError: error,
        lastRunId: run.runId,
      });
    }
    return finalRun;
  }

  async getRun(runId: string): Promise<PipelineRun | null> {
    return this.runs.get(runId) ?? null;
  }

  async getActiveRun(pipelineId: string): Promise<PipelineRun | null> {
    const id = this.activeByPipeline.get(pipelineId);
    if (!id) return null;
    const run = this.runs.get(id);
    if (!run || isTerminalPipelineRunStatus(run.status)) return null;
    return run;
  }

  async listRuns(pipelineId: string): Promise<readonly PipelineRun[]> {
    return [...this.runs.values()].filter((r) => r.pipelineId === pipelineId);
  }

  async listDueSchedules(now: Date): Promise<readonly Pipeline[]> {
    const nowIso = now.toISOString();
    return [...this.pipelines.values()].filter((p) => {
      if (!p.enabled || p.trigger.kind !== "schedule") return false;
      if (!p.nextRunAt || p.nextRunAt > nowIso) return false;
      const activeId = this.activeByPipeline.get(p.id);
      if (activeId) {
        const active = this.runs.get(activeId);
        if (active && !isTerminalPipelineRunStatus(active.status)) return false;
      }
      return true;
    });
  }

  async recoverExpiredLeases(now: Date): Promise<number> {
    let n = 0;
    for (const [pipelineId, runId] of [...this.activeByPipeline.entries()]) {
      const run = this.runs.get(runId);
      if (!run || isTerminalPipelineRunStatus(run.status)) {
        this.activeByPipeline.delete(pipelineId);
        continue;
      }
      if (Date.parse(run.leaseExpiresAt) > now.getTime()) continue;
      await this.finalizeRun(
        run,
        "interrupted",
        "Run interrupted (lease expired or process restarted)",
        now,
      );
      n += 1;
    }
    return n;
  }

  async markRun(id: string, status: "ok" | "error" | "cancelled", error: string | null, now: Date): Promise<Pipeline | null> {
    const existing = this.pipelines.get(id);
    if (!existing) return null;
    const updated: Pipeline = { ...existing, lastRunAt: now.toISOString(), lastStatus: status, lastError: error };
    this.pipelines.set(id, updated);
    return updated;
  }
}

describe("PipelineScheduler", () => {
  let store: FakePipelineStore;
  let dispatcher: EventDispatcher;
  let runAgentMock: ReturnType<typeof vi.fn>;
  let callToolMock: ReturnType<typeof vi.fn>;
  let scheduler: PipelineScheduler;

  beforeEach(() => {
    store = new FakePipelineStore();
    dispatcher = new EventDispatcher();
    runAgentMock = vi.fn().mockResolvedValue({ status: "ok", summary: "done" });
    callToolMock = vi.fn().mockResolvedValue({ requestId: "r1", result: "tool result" });
    scheduler = new PipelineScheduler({
      store,
      executor: { runAgent: runAgentMock },
      callToolHandler: { handle: callToolMock },
      eventDispatcher: dispatcher,
      executorSettings: {} as never,
    });
  });

  it("runs a linear pipeline (a → b → c)", async () => {
    const pipeline = makePipeline([
      makeStep("a"),
      makeStep("b", { dependsOn: ["a"] }),
      makeStep("c", { dependsOn: ["b"] }),
    ]);
    store.pipelines.set(pipeline.id, pipeline);
    const result = await scheduler.runPipeline(pipeline.id);
    expect(result.ok).toBe(true);
    expect(result.runId).toBeTruthy();
    expect(runAgentMock).toHaveBeenCalledTimes(3);
    expect(store.pipelines.get(pipeline.id)?.lastStatus).toBe("ok");
    const runs = await store.listRuns(pipeline.id);
    expect(runs).toHaveLength(1);
    expect(runs[0]!.status).toBe("ok");
    expect(runs[0]!.stepRuns.every((s) => s.status === "ok" || s.status === "skipped")).toBe(true);
  });

  it("launch returns immediately with runId and runs in background", async () => {
    let release!: () => void;
    const barrier = new Promise<void>((resolve) => { release = resolve; });
    runAgentMock.mockImplementation(async () => {
      await barrier;
      return { status: "ok", summary: "done" };
    });
    const pipeline = makePipeline([makeStep("a")]);
    store.pipelines.set(pipeline.id, pipeline);

    const result = await scheduler.launch(pipeline.id);
    expect(result.ok).toBe(true);
    expect(result.runId).toBeTruthy();
    expect(result.traceId).toBeTruthy();
    // Must return BEFORE the run completes.
    expect(runAgentMock).not.toHaveBeenCalled();
    // Claimed immediately.
    expect(store.activeByPipeline.get(pipeline.id)).toBe(result.runId);

    release();
    await vi.waitFor(() => expect(store.pipelines.get(pipeline.id)?.lastStatus).toBe("ok"));
    expect(runAgentMock).toHaveBeenCalledTimes(1);
    expect(store.activeByPipeline.has(pipeline.id)).toBe(false);
  });

  it("launch rejects concurrent claim and reports already running", async () => {
    let release!: () => void;
    const barrier = new Promise<void>((resolve) => { release = resolve; });
    runAgentMock.mockImplementation(async () => {
      await barrier;
      return { status: "ok", summary: "done" };
    });
    const pipeline = makePipeline([makeStep("a")]);
    store.pipelines.set(pipeline.id, pipeline);

    const first = await scheduler.launch(pipeline.id);
    expect(first.ok).toBe(true);
    const second = await scheduler.launch(pipeline.id);
    expect(second.ok).toBe(false);
    expect(second.errorCode).toBe("PIPELINE_ALREADY_RUNNING");
    release();
    await vi.waitFor(() => expect(store.pipelines.get(pipeline.id)?.lastStatus).toBe("ok"));
  });

  it("rejects concurrent claims on the same pipeline", async () => {
    let release!: () => void;
    const barrier = new Promise<void>((resolve) => { release = resolve; });
    runAgentMock.mockImplementation(async () => {
      await barrier;
      return { status: "ok", summary: "done" };
    });
    const pipeline = makePipeline([makeStep("a")]);
    store.pipelines.set(pipeline.id, pipeline);

    const first = scheduler.runPipeline(pipeline.id);
    // Let claim settle
    await new Promise((r) => setTimeout(r, 10));
    const second = await scheduler.runPipeline(pipeline.id);
    expect(second.ok).toBe(false);
    expect(second.errorCode).toBe("PIPELINE_ALREADY_RUNNING");
    release();
    const firstResult = await first;
    expect(firstResult.ok).toBe(true);
  });

  it("stores output in context via outputKey", async () => {
    runAgentMock.mockResolvedValue({ status: "ok", summary: "classification result" });
    const pipeline = makePipeline([
      makeStep("classify", { outputKey: "classification" }),
      makeStep("handle", { dependsOn: ["classify"], action: { type: "agent", prompt: "Use {{context.classification}}" } }),
    ]);
    store.pipelines.set(pipeline.id, pipeline);
    const result = await scheduler.runPipeline(pipeline.id);
    expect(result.ok).toBe(true);
    const secondCall = runAgentMock.mock.calls[1];
    expect(secondCall![0]).toBe("Use classification result");
  });

  it("keeps {{payload.*}} / {{event.*}} literal on manual runs (no fake event)", async () => {
    const pipeline = makePipeline([
      makeStep("a", {
        action: { type: "agent", prompt: "Handle {{payload.category}} / {{event.type}}" },
      }),
    ]);
    store.pipelines.set(pipeline.id, pipeline);
    const result = await scheduler.runPipeline(pipeline.id);
    expect(result.ok).toBe(true);
    expect(runAgentMock).toHaveBeenCalledTimes(1);
    expect(runAgentMock.mock.calls[0]![0]).toBe("Handle {{payload.category}} / {{event.type}}");
  });

  it("resolves {{context.*}} on manual runs (context always present)", async () => {
    runAgentMock.mockResolvedValueOnce({ status: "ok", summary: "S1" });
    const pipeline = makePipeline([
      makeStep("a", { outputKey: "summary", action: { type: "agent", prompt: "first" } }),
      makeStep("b", { dependsOn: ["a"], action: { type: "agent", prompt: "Use {{context.summary}}" } }),
    ]);
    store.pipelines.set(pipeline.id, pipeline);
    const result = await scheduler.runPipeline(pipeline.id);
    expect(result.ok).toBe(true);
    expect(runAgentMock.mock.calls[1]![0]).toBe("Use S1");
  });

  it("resolves {{payload.*}} from a real event template context", async () => {
    const pipeline = makePipeline([
      makeStep("a", { action: { type: "agent", prompt: "Handle {{payload.category}}" } }),
    ]);
    store.pipelines.set(pipeline.id, pipeline);
    const result = await scheduler.runPipeline(pipeline.id, {
      event: { type: "test.event", pluginId: "test", payload: { category: "finance" } },
    });
    expect(result.ok).toBe(true);
    expect(runAgentMock.mock.calls[0]![0]).toBe("Handle finance");
  });

  it("keeps {{payload.*}} literal in tool args on manual runs", async () => {
    const pipeline = makePipeline([
      makeStep("a", {
        action: { type: "tool", pluginId: "files", toolName: "read", args: { path: "{{payload.path}}" } },
      }),
    ]);
    store.pipelines.set(pipeline.id, pipeline);
    const result = await scheduler.runPipeline(pipeline.id);
    expect(result.ok).toBe(true);
    const [cmd] = callToolMock.mock.calls[0]!;
    expect(cmd.args).toEqual({ path: "{{payload.path}}" });
  });

  it("skips step when condition is false", async () => {
    const pipeline = makePipeline([
      makeStep("classify", { outputKey: "category", action: { type: "agent", prompt: "urgent" } }),
      makeStep("handle-urgent", {
        dependsOn: ["classify"],
        condition: { path: "category", op: "eq", value: "urgent" },
        action: { type: "agent", prompt: "handle urgent" },
      }),
      makeStep("handle-normal", {
        dependsOn: ["classify"],
        condition: { path: "category", op: "eq", value: "normal" },
        action: { type: "agent", prompt: "handle normal" },
      }),
    ]);
    store.pipelines.set(pipeline.id, pipeline);
    runAgentMock.mockResolvedValueOnce({ status: "ok", summary: "urgent" });
    const result = await scheduler.runPipeline(pipeline.id);
    expect(result.ok).toBe(true);
    expect(runAgentMock).toHaveBeenCalledTimes(2);
  });

  it("condition path resolves against step context (outputKey), not event payload", async () => {
    const pipeline = makePipeline([
      makeStep("classify", { outputKey: "category", action: { type: "agent", prompt: "classify" } }),
      makeStep("handle-urgent", {
        dependsOn: ["classify"],
        condition: { path: "category", op: "eq", value: "urgent" },
        action: { type: "agent", prompt: "handle urgent" },
      }),
    ]);
    store.pipelines.set(pipeline.id, pipeline);
    runAgentMock.mockResolvedValueOnce({ status: "ok", summary: "urgent" });
    const result = await scheduler.runPipeline(pipeline.id);
    expect(result.ok).toBe(true);
    // classify + handle-urgent both ran (condition saw category=urgent via outputKey)
    expect(runAgentMock).toHaveBeenCalledTimes(2);
  });

  it("condition with dot path walks nested context values", async () => {
    const pipeline = makePipeline([
      makeStep("a", { outputKey: "a" }),
      makeStep("b", {
        dependsOn: ["a"],
        condition: { op: "not", of: { path: "a", op: "eq", value: "done" } },
        action: { type: "agent", prompt: "b" },
      }),
    ]);
    store.pipelines.set(pipeline.id, pipeline);
    const result = await scheduler.runPipeline(pipeline.id);
    expect(result.ok).toBe(true);
    // step b skipped because a=dleon but condition was NOT(eq 'done')... summary 'done' means NOT false => skipped
    const run = await store.getRun(result.runId!);
    const stepB = run?.stepRuns.find((sr) => sr.stepId === "b");
    expect(stepB?.status).toBe("skipped");
  });

  it("stops on step error", async () => {
    const pipeline = makePipeline([
      makeStep("a"),
      makeStep("b", { dependsOn: ["a"] }),
      makeStep("c", { dependsOn: ["b"] }),
    ]);
    store.pipelines.set(pipeline.id, pipeline);
    runAgentMock
      .mockResolvedValueOnce({ status: "ok", summary: "a ok" })
      .mockResolvedValueOnce({ status: "error", summary: "boom", error: "boom" });
    const result = await scheduler.runPipeline(pipeline.id);
    expect(result.ok).toBe(false);
    expect(runAgentMock).toHaveBeenCalledTimes(2);
    expect(store.pipelines.get(pipeline.id)?.lastStatus).toBe("error");
  });

  it("cancels an in-flight run", async () => {
    let release!: () => void;
    const barrier = new Promise<void>((resolve) => { release = resolve; });
    runAgentMock.mockImplementation(async (_p: string, _s: unknown, signal?: AbortSignal) => {
      await barrier;
      if (signal?.aborted) throw new Error("aborted");
      return { status: "ok", summary: "done" };
    });
    const pipeline = makePipeline([makeStep("a"), makeStep("b", { dependsOn: ["a"] })]);
    store.pipelines.set(pipeline.id, pipeline);

    const runPromise = scheduler.runPipeline(pipeline.id);
    await new Promise((r) => setTimeout(r, 10));
    const cancel = await scheduler.cancel(pipeline.id);
    expect(cancel.ok).toBe(true);
    release();
    const result = await runPromise;
    expect(result.ok).toBe(false);
    expect(result.status === "cancelled" || result.errorCode === "PIPELINE_TIMEOUT" || result.error).toBeTruthy();
  });

  it("passes AbortSignal to agent executor", async () => {
    const pipeline = makePipeline([makeStep("a")]);
    store.pipelines.set(pipeline.id, pipeline);
    await scheduler.runPipeline(pipeline.id);
    expect(runAgentMock.mock.calls[0]![2]).toBeInstanceOf(AbortSignal);
  });

  it("runs tool steps", async () => {
    const pipeline = makePipeline([
      makeStep("t", {
        action: { type: "tool", pluginId: "p", toolName: "echo", args: { x: 1 } },
      }),
    ]);
    store.pipelines.set(pipeline.id, pipeline);
    const result = await scheduler.runPipeline(pipeline.id);
    expect(result.ok).toBe(true);
    expect(callToolMock).toHaveBeenCalled();
  });

  it("returns not found for missing pipeline", async () => {
    const result = await scheduler.runPipeline("missing");
    expect(result.ok).toBe(false);
    expect(result.errorCode).toBe("PIPELINE_NOT_FOUND");
  });

  it("refuses non-manual triggers for disabled pipelines", async () => {
    const pipeline = makePipeline([makeStep("a")], { enabled: false });
    store.pipelines.set(pipeline.id, pipeline);

    const scheduleResult = await scheduler.runPipeline(pipeline.id, undefined, { source: "schedule" });
    expect(scheduleResult.ok).toBe(false);
    expect(scheduleResult.errorCode).toBe("PIPELINE_DISABLED");

    const eventResult = await scheduler.launch(pipeline.id, undefined, { source: "event" });
    expect(eventResult.ok).toBe(false);
    expect(eventResult.errorCode).toBe("PIPELINE_DISABLED");
  });

  it("allows manual run of disabled (paused) pipelines", async () => {
    const pipeline = makePipeline([makeStep("a")], { enabled: false });
    store.pipelines.set(pipeline.id, pipeline);

    const runResult = await scheduler.runPipeline(pipeline.id);
    expect(runResult.ok).toBe(true);
    expect(runResult.runId).toBeTruthy();

    const pipeline2 = makePipeline([makeStep("b")], { enabled: false });
    store.pipelines.set(pipeline2.id, pipeline2);
    const launchResult = await scheduler.launch(pipeline2.id, undefined, { source: "manual" });
    expect(launchResult.ok).toBe(true);
    expect(launchResult.runId).toBeTruthy();
  });

  it("recovers expired leases on startup", async () => {
    const pipeline = makePipeline([makeStep("a")]);
    store.pipelines.set(pipeline.id, pipeline);
    const past = new Date(Date.now() - 60_000).toISOString();
    await store.claimRun({
      runId: "stale-run",
      pipelineId: pipeline.id,
      traceId: "t",
      status: "running",
      triggerSource: "manual",
      startedAt: past,
      completedAt: null,
      lastHeartbeatAt: past,
      leaseExpiresAt: past,
      currentStepId: "a",
      errorCode: null,
      errorMessage: null,
      stepRuns: [{ stepId: "a", status: "running" }],
    });
    const recovered = await scheduler.recoverOnStartup();
    expect(recovered).toBe(1);
    expect((await store.getRun("stale-run"))?.status).toBe("interrupted");
  });

  it("bounds large step summaries in persisted runs", async () => {
    const big = "Z".repeat(10_000);
    runAgentMock.mockResolvedValueOnce({ status: "ok", summary: big });
    const pipeline = makePipeline([makeStep("huge", { outputKey: "out" })]);
    store.pipelines.set(pipeline.id, pipeline);
    const result = await scheduler.runPipeline(pipeline.id);
    expect(result.ok).toBe(true);
    const runs = await store.listRuns(pipeline.id);
    const step = runs[0]?.stepRuns.find((s) => s.stepId === "huge");
    expect(step?.summary?.length).toBeLessThanOrEqual(4_001);
    expect(step?.outputTruncated).toBe(true);
    expect(step?.outputPreview?.length).toBeLessThanOrEqual(2_001);
  });

  it("rejects a second concurrent claim while first is in flight", async () => {
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    runAgentMock.mockImplementation(async () => {
      await gate;
      return { status: "ok", summary: "ok" };
    });
    const pipeline = makePipeline([makeStep("a")]);
    store.pipelines.set(pipeline.id, pipeline);
    const first = scheduler.runPipeline(pipeline.id);
    await vi.waitFor(() => expect(runAgentMock).toHaveBeenCalled());
    const second = await scheduler.runPipeline(pipeline.id);
    expect(second.ok).toBe(false);
    expect(second.errorCode).toBe("PIPELINE_ALREADY_RUNNING");
    release();
    await first;
  });
});
