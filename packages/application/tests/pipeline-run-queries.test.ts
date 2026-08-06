import { describe, expect, it } from "vitest";
import { ListPipelineRunsHandler } from "../src/job/queries/list-pipeline-runs/list-pipeline-runs.handler.js";
import { GetPipelineRunHandler } from "../src/job/queries/get-pipeline-run/get-pipeline-run.handler.js";
import type { Pipeline, PipelineRun } from "../src/job/pipeline-model.js";
import type { PipelineStorePort } from "../src/job/ports/pipeline-store.port.js";

function makeStore(run: PipelineRun): PipelineStorePort {
  const pipeline: Pipeline = {
    id: run.pipelineId,
    name: "P",
    enabled: true,
    trigger: { kind: "event", pattern: "x" },
    steps: [{ id: "a", name: "A", action: { type: "agent", prompt: "p" } }],
    createdAt: run.startedAt,
    nextRunAt: null,
    lastRunAt: null,
    lastStatus: "ok",
    lastError: null,
  };
  return {
    create: async (p) => p,
    update: async (p) => p,
    get: async (id) => (id === pipeline.id ? pipeline : null),
    list: async () => [pipeline],
    remove: async () => undefined,
    claimRun: async () => null,
    updateRun: async (r) => r,
    finalizeRun: async (r) => r,
    getRun: async (id) => (id === run.runId ? run : null),
    getActiveRun: async () => null,
    listRuns: async () => [run],
    listDueSchedules: async () => [],
    recoverExpiredLeases: async () => 0,
    markRun: async () => null,
  };
}

describe("pipeline run queries", () => {
  const run: PipelineRun = {
    runId: "run-1",
    pipelineId: "p1",
    traceId: "t1",
    status: "ok",
    triggerSource: "manual",
    startedAt: "2025-01-01T00:00:00.000Z",
    completedAt: "2025-01-01T00:01:00.000Z",
    lastHeartbeatAt: "2025-01-01T00:01:00.000Z",
    leaseExpiresAt: "2025-01-01T00:10:00.000Z",
    currentStepId: "a",
    errorCode: null,
    errorMessage: null,
    stepRuns: [
      {
        stepId: "a",
        status: "ok",
        summary: "x".repeat(2_000),
        outputPreview: "y".repeat(1_500),
        outputTruncated: true,
        startedAt: "2025-01-01T00:00:00.000Z",
        completedAt: "2025-01-01T00:01:00.000Z",
      },
    ],
  };

  it("compacts list payloads by default", async () => {
    const handler = new ListPipelineRunsHandler(makeStore(run));
    const result = await handler.handle({ kind: "list-pipeline-runs", pipelineId: "p1" });
    const step = result.runs[0]!.stepRuns[0]!;
    expect(step.outputPreview).toBeUndefined();
    expect(step.summary?.length).toBeLessThanOrEqual(500);
    expect(step.outputTruncated).toBe(true);
  });

  it("returns full bounded previews when includeBody is true", async () => {
    const handler = new ListPipelineRunsHandler(makeStore(run));
    const result = await handler.handle({
      kind: "list-pipeline-runs",
      pipelineId: "p1",
      includeBody: true,
    });
    expect(result.runs[0]!.stepRuns[0]!.outputPreview?.length).toBe(1_500);
  });

  it("gets a single run by id", async () => {
    const handler = new GetPipelineRunHandler(makeStore(run));
    const result = await handler.handle({ kind: "get-pipeline-run", runId: "run-1", includeBody: true });
    expect(result.run?.runId).toBe("run-1");
  });
});
