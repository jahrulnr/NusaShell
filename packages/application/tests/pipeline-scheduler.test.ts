import { describe, expect, it, beforeEach, vi } from "vitest";
import { PipelineScheduler } from "../src/job/services/pipeline-scheduler.js";
import { EventDispatcher } from "../src/events/event-dispatcher.js";
import type { Pipeline, PipelineStorePort, PipelineStep, PipelineStepResult } from "../src/job/pipeline-model.js";

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
    lastRunAt: null,
    lastStatus: null,
    lastError: null,
    ...overrides,
  };
}

class FakePipelineStore implements PipelineStorePort {
  pipelines = new Map<string, Pipeline>();

  async create(p: Pipeline): Promise<Pipeline> { this.pipelines.set(p.id, p); return p; }
  async update(p: Pipeline): Promise<Pipeline> { this.pipelines.set(p.id, p); return p; }
  async get(id: string): Promise<Pipeline | null> { return this.pipelines.get(id) ?? null; }
  async list(): Promise<readonly Pipeline[]> { return [...this.pipelines.values()]; }
  async remove(id: string): Promise<void> { this.pipelines.delete(id); }
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
    expect(runAgentMock).toHaveBeenCalledTimes(3);
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
    // Second step's prompt should have been resolved with context
    const secondCall = runAgentMock.mock.calls[1];
    expect(secondCall[0]).toBe("Use classification result");
  });

  it("skips step when condition is false", async () => {
    const pipeline = makePipeline([
      makeStep("classify", { outputKey: "category", action: { type: "agent", prompt: "urgent" } }),
      makeStep("handle-urgent", {
        dependsOn: ["classify"],
        condition: { path: "payload.category", op: "eq", value: "urgent" },
        action: { type: "agent", prompt: "handle urgent" },
      }),
      makeStep("handle-normal", {
        dependsOn: ["classify"],
        condition: { path: "payload.category", op: "eq", value: "normal" },
        action: { type: "agent", prompt: "handle normal" },
      }),
    ]);
    store.pipelines.set(pipeline.id, pipeline);
    // classify returns "urgent" → handle-urgent fires, handle-normal skipped
    runAgentMock.mockResolvedValueOnce({ status: "ok", summary: "urgent" });
    const result = await scheduler.runPipeline(pipeline.id);
    expect(result.ok).toBe(true);
    // classify + handle-urgent = 2 calls (handle-normal skipped)
    expect(runAgentMock).toHaveBeenCalledTimes(2);
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
      .mockResolvedValueOnce({ status: "error", summary: "b failed", error: "b failed" });
    const result = await scheduler.runPipeline(pipeline.id);
    expect(result.ok).toBe(false);
    expect(result.error).toMatch(/step "b" failed/i);
    // c should not run
    expect(runAgentMock).toHaveBeenCalledTimes(2);
  });

  it("returns error for non-existent pipeline", async () => {
    const result = await scheduler.runPipeline("nonexistent");
    expect(result.ok).toBe(false);
    expect(result.error).toMatch(/not found/i);
  });

  it("returns error for disabled pipeline", async () => {
    const pipeline = makePipeline([makeStep("a")], { enabled: false });
    store.pipelines.set(pipeline.id, pipeline);
    const result = await scheduler.runPipeline(pipeline.id);
    expect(result.ok).toBe(false);
    expect(result.error).toMatch(/disabled/i);
  });

  it("runs diamond DAG (a → b,c → d)", async () => {
    const pipeline = makePipeline([
      makeStep("a"),
      makeStep("b", { dependsOn: ["a"] }),
      makeStep("c", { dependsOn: ["a"] }),
      makeStep("d", { dependsOn: ["b", "c"] }),
    ]);
    store.pipelines.set(pipeline.id, pipeline);
    const result = await scheduler.runPipeline(pipeline.id);
    expect(result.ok).toBe(true);
    expect(runAgentMock).toHaveBeenCalledTimes(4);
  });

  it("calls tool for tool-type step", async () => {
    const pipeline = makePipeline([
      makeStep("sync", {
        action: {
          type: "tool",
          pluginId: "nusashell.notes",
          toolName: "notes_sync",
          args: { direction: "push" },
        },
      }),
    ]);
    store.pipelines.set(pipeline.id, pipeline);
    const result = await scheduler.runPipeline(pipeline.id);
    expect(result.ok).toBe(true);
    expect(callToolMock).toHaveBeenCalledTimes(1);
    expect(runAgentMock).not.toHaveBeenCalled();
  });
});
