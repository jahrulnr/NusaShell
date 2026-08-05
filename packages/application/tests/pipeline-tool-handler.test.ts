import { describe, expect, it, beforeEach, vi } from "vitest";
import { execPipeline } from "../src/agent/services/pipeline-tool-handler.js";
import type { PipelineStorePort, PipelineScheduler, Pipeline } from "../src/job/pipeline-model.js";

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

describe("pipeline tool handler", () => {
  let store: FakePipelineStore;
  let scheduler: PipelineScheduler;

  beforeEach(() => {
    store = new FakePipelineStore();
    scheduler = {
      runPipeline: vi.fn().mockResolvedValue({ ok: true }),
    } as unknown as PipelineScheduler;
  });

  it("returns not-configured when store is undefined", async () => {
    const result = await execPipeline(undefined, scheduler, { action: "list" });
    expect(result).toHaveProperty("ok", false);
  });

  it("lists pipelines (empty)", async () => {
    const result = await execPipeline(store, scheduler, { action: "list" });
    expect(result).toMatchObject({ ok: true, data: [], meta: { count: 0 } });
  });

  it("adds a pipeline with event trigger", async () => {
    const result = await execPipeline(store, scheduler, {
      action: "add",
      name: "Test pipeline",
      trigger: { kind: "event", pattern: "mail.new" },
      steps: [
        { id: "a", name: "Step A", action: { type: "agent", prompt: "test" } },
        { id: "b", name: "Step B", dependsOn: ["a"], action: { type: "agent", prompt: "test b" } },
      ],
    });
    expect(result).toHaveProperty("ok", true);
    expect(store.pipelines.size).toBe(1);
    const pipeline = [...store.pipelines.values()][0]!;
    expect(pipeline.name).toBe("Test pipeline");
    expect(pipeline.trigger.kind).toBe("event");
    expect(pipeline.steps.length).toBe(2);
  });

  it("adds a pipeline with schedule trigger", async () => {
    const result = await execPipeline(store, scheduler, {
      action: "add",
      name: "Scheduled pipeline",
      schedule: "every 1h",
      steps: [
        { id: "a", name: "Step A", action: { type: "agent", prompt: "test" } },
      ],
    });
    expect(result).toHaveProperty("ok", true);
    const pipeline = [...store.pipelines.values()][0]!;
    expect(pipeline.trigger.kind).toBe("schedule");
  });

  it("rejects empty steps", async () => {
    const result = await execPipeline(store, scheduler, {
      action: "add",
      name: "Empty",
      trigger: { kind: "event", pattern: "test" },
      steps: [],
    });
    expect(result).toHaveProperty("ok", false);
  });

  it("rejects cycle in steps", async () => {
    const result = await execPipeline(store, scheduler, {
      action: "add",
      name: "Cyclic",
      trigger: { kind: "event", pattern: "test" },
      steps: [
        { id: "a", name: "A", dependsOn: ["b"], action: { type: "agent", prompt: "a" } },
        { id: "b", name: "B", dependsOn: ["a"], action: { type: "agent", prompt: "b" } },
      ],
    });
    expect(result).toHaveProperty("ok", false);
  });

  it("updates a pipeline", async () => {
    const addResult = await execPipeline(store, scheduler, {
      action: "add",
      name: "Original",
      trigger: { kind: "event", pattern: "test" },
      steps: [{ id: "a", name: "A", action: { type: "agent", prompt: "a" } }],
    });
    const id = (addResult as { data: { id: string } }).data.id;
    const result = await execPipeline(store, scheduler, {
      action: "update",
      id,
      name: "Updated",
    });
    expect(result).toHaveProperty("ok", true);
    expect(store.pipelines.get(id)!.name).toBe("Updated");
  });

  it("removes a pipeline", async () => {
    const addResult = await execPipeline(store, scheduler, {
      action: "add",
      name: "To remove",
      trigger: { kind: "event", pattern: "test" },
      steps: [{ id: "a", name: "A", action: { type: "agent", prompt: "a" } }],
    });
    const id = (addResult as { data: { id: string } }).data.id;
    const result = await execPipeline(store, scheduler, { action: "remove", id });
    expect(result).toHaveProperty("ok", true);
    expect(store.pipelines.has(id)).toBe(false);
  });

  it("runs a pipeline", async () => {
    const addResult = await execPipeline(store, scheduler, {
      action: "add",
      name: "To run",
      trigger: { kind: "event", pattern: "test" },
      steps: [{ id: "a", name: "A", action: { type: "agent", prompt: "a" } }],
    });
    const id = (addResult as { data: { id: string } }).data.id;
    const result = await execPipeline(store, scheduler, { action: "run", id });
    expect(result).toHaveProperty("ok", true);
    expect(scheduler.runPipeline).toHaveBeenCalledWith(id);
  });

  it("returns error for non-existent pipeline update", async () => {
    const result = await execPipeline(store, scheduler, { action: "update", id: "nonexistent", name: "X" });
    expect(result).toHaveProperty("ok", false);
  });

  it("parses tool-type step action", async () => {
    const result = await execPipeline(store, scheduler, {
      action: "add",
      name: "Tool pipeline",
      trigger: { kind: "event", pattern: "test" },
      steps: [
        {
          id: "sync",
          name: "Sync",
          action: { type: "tool", pluginId: "nusashell.notes", toolName: "create", args: { title: "test" } },
        },
      ],
    });
    expect(result).toHaveProperty("ok", true);
    const pipeline = [...store.pipelines.values()][0]!;
    expect(pipeline.steps[0]!.action.type).toBe("tool");
  });

  it("parses condition with or nesting", async () => {
    const result = await execPipeline(store, scheduler, {
      action: "add",
      name: "Conditional",
      trigger: { kind: "event", pattern: "test" },
      steps: [
        {
          id: "a",
          name: "A",
          action: { type: "agent", prompt: "a" },
          condition: { op: "or", any: [
            { path: "payload.x", op: "eq", value: "1" },
            { path: "payload.y", op: "eq", value: "2" },
          ] },
        },
      ],
    });
    const r = result as { ok: boolean; error?: { code: string; message: string } };
    if (!r.ok) throw new Error(`${r.error?.code}: ${r.error?.message}`);
    expect(r.ok).toBe(true);
  });
});
