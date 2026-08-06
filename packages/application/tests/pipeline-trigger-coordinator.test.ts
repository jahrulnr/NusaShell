import { describe, expect, it, vi, beforeEach } from "vitest";
import { PipelineTriggerCoordinator } from "../src/job/services/pipeline-trigger-coordinator.js";
import { EventDispatcher } from "../src/events/event-dispatcher.js";
import type { Pipeline, PipelineRun, PipelineStatus } from "../src/job/pipeline-model.js";
import { isTerminalPipelineRunStatus } from "../src/job/pipeline-model.js";
import type { PipelineStorePort } from "../src/job/ports/pipeline-store.port.js";
import type { PipelineScheduler } from "../src/job/services/pipeline-scheduler.js";

class FakeStore implements PipelineStorePort {
  pipelines = new Map<string, Pipeline>();
  runs = new Map<string, PipelineRun>();
  activeByPipeline = new Map<string, string>();

  async create(p: Pipeline): Promise<Pipeline> {
    this.pipelines.set(p.id, p);
    return p;
  }
  async update(p: Pipeline): Promise<Pipeline> {
    this.pipelines.set(p.id, p);
    return p;
  }
  async get(id: string): Promise<Pipeline | null> {
    return this.pipelines.get(id) ?? null;
  }
  async list(): Promise<readonly Pipeline[]> {
    return [...this.pipelines.values()];
  }
  async remove(id: string): Promise<void> {
    this.pipelines.delete(id);
  }
  async claimRun(): Promise<PipelineRun | null> {
    return null;
  }
  async updateRun(run: PipelineRun): Promise<PipelineRun | null> {
    return run;
  }
  async finalizeRun(
    run: PipelineRun,
    status: Extract<PipelineStatus, "ok" | "error" | "cancelled" | "interrupted">,
    error: string | null,
  ): Promise<PipelineRun | null> {
    return { ...run, status, errorMessage: error };
  }
  async getRun(): Promise<PipelineRun | null> {
    return null;
  }
  async getActiveRun(): Promise<PipelineRun | null> {
    return null;
  }
  async listRuns(): Promise<readonly PipelineRun[]> {
    return [];
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
  async recoverExpiredLeases(): Promise<number> {
    return 0;
  }
  async markRun(): Promise<Pipeline | null> {
    return null;
  }
}

describe("PipelineTriggerCoordinator", () => {
  let store: FakeStore;
  let dispatcher: EventDispatcher;
  let runPipeline: ReturnType<typeof vi.fn>;
  let coordinator: PipelineTriggerCoordinator;
  const NOW = new Date("2025-01-01T12:00:00.000Z");

  beforeEach(() => {
    store = new FakeStore();
    dispatcher = new EventDispatcher();
    runPipeline = vi.fn().mockResolvedValue({ ok: true, runId: "r1" });
    coordinator = new PipelineTriggerCoordinator({
      store,
      scheduler: { runPipeline } as unknown as PipelineScheduler,
      eventDispatcher: dispatcher,
      now: () => NOW,
    });
  });

  it("fires due schedule pipelines with source schedule", async () => {
    const pipeline: Pipeline = {
      id: "p1",
      name: "Hourly",
      enabled: true,
      trigger: { kind: "schedule", schedule: { kind: "interval", minutes: 60 } },
      steps: [{ id: "a", name: "A", action: { type: "agent", prompt: "hi" } }],
      createdAt: "2025-01-01T00:00:00.000Z",
      nextRunAt: "2025-01-01T11:00:00.000Z",
      lastRunAt: null,
      lastStatus: null,
      lastError: null,
    };
    await store.create(pipeline);
    await coordinator.tick();
    expect(runPipeline).toHaveBeenCalledWith("p1", undefined, { source: "schedule" });
  });

  it("marks missed one-shot past grace without running", async () => {
    const pipeline: Pipeline = {
      id: "p2",
      name: "Once",
      enabled: true,
      trigger: {
        kind: "schedule",
        schedule: { kind: "once", runAt: "2024-12-31T00:00:00.000Z" },
      },
      steps: [{ id: "a", name: "A", action: { type: "agent", prompt: "hi" } }],
      createdAt: "2024-12-01T00:00:00.000Z",
      nextRunAt: "2024-12-31T00:00:00.000Z",
      lastRunAt: null,
      lastStatus: null,
      lastError: null,
    };
    await store.create(pipeline);
    await coordinator.tick();
    expect(runPipeline).not.toHaveBeenCalled();
    const updated = await store.get("p2");
    expect(updated?.lastStatus).toBe("error");
    expect(updated?.lastError).toMatch(/missed/i);
    expect(updated?.nextRunAt).toBeNull();
  });

  it("skips event-triggered pipelines in the schedule tick", async () => {
    await store.create({
      id: "p3",
      name: "Event",
      enabled: true,
      trigger: { kind: "event", pattern: "mail.new" },
      steps: [{ id: "a", name: "A", action: { type: "agent", prompt: "hi" } }],
      createdAt: "2025-01-01T00:00:00.000Z",
      nextRunAt: null,
      lastRunAt: null,
      lastStatus: null,
      lastError: null,
    });
    await coordinator.tick();
    expect(runPipeline).not.toHaveBeenCalled();
  });
});
