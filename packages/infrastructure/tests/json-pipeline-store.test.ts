import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it, afterEach } from "vitest";
import { JsonPipelineStore } from "../src/persistence/json/json-pipeline-store.js";
import type { Pipeline, PipelineRun } from "@nusashell/application";

function makePipeline(id: string): Pipeline {
  return {
    id,
    name: `Pipeline ${id}`,
    enabled: true,
    trigger: { kind: "event", pattern: "test.event" },
    steps: [
      {
        id: "a",
        name: "Step a",
        action: { type: "agent", prompt: "test" },
      },
    ],
    createdAt: "2025-01-01T00:00:00.000Z",
    nextRunAt: null,
    lastRunAt: null,
    lastStatus: null,
    lastError: null,
  };
}

function makeRun(runId: string, pipelineId: string): PipelineRun {
  const now = new Date().toISOString();
  return {
    runId,
    pipelineId,
    traceId: runId,
    status: "claimed",
    triggerSource: "manual",
    startedAt: now,
    completedAt: null,
    lastHeartbeatAt: now,
    leaseExpiresAt: new Date(Date.now() + 60_000).toISOString(),
    currentStepId: null,
    errorCode: null,
    errorMessage: null,
    stepRuns: [],
  };
}

describe("JsonPipelineStore", () => {
  let root: string;

  afterEach(async () => {
    if (root) await rm(root, { recursive: true, force: true });
  });

  it("creates, claims, and finalizes a run", async () => {
    root = await mkdtemp(join(tmpdir(), "pipelines-test-"));
    const store = new JsonPipelineStore(root);
    const pipeline = makePipeline("p1");
    await store.create(pipeline);

    const claimed = await store.claimRun(makeRun("r1", "p1"));
    expect(claimed?.status).toBe("claimed");

    await store.finalizeRun(claimed!, "ok", null, new Date());
    const run = await store.getRun("r1");
    expect(run?.status).toBe("ok");
    const pipelines = await store.list();
    expect(pipelines[0]?.lastStatus).toBe("ok");
  });

  it("remove() drops the pipeline's runs together with the definition", async () => {
    root = await mkdtemp(join(tmpdir(), "pipelines-test-"));
    const store = new JsonPipelineStore(root);
    await store.create(makePipeline("p1"));
    const claimed = await store.claimRun(makeRun("r1", "p1"));
    await store.finalizeRun(claimed!, "ok", null, new Date());
    // Sanity: the run is present before delete.
    expect(await store.listRuns("p1")).toHaveLength(1);

    await store.remove("p1");
    expect(await store.get("p1")).toBeNull();
    expect(await store.listRuns("p1")).toHaveLength(0);
    expect(await store.getRun("r1")).toBeNull();
  });

  it("recovers runs with an expired lease as interrupted", async () => {
    root = await mkdtemp(join(tmpdir(), "pipelines-test-"));
    const store = new JsonPipelineStore(root);
    await store.create(makePipeline("p1"));
    await store.claimRun(makeRun("r1", "p1"));
    // Force the lease into the past.
    const stale = (await store.getRun("r1"))!;
    const past = new Date(Date.now() - 10 * 60_000).toISOString();
    await (store as unknown as { state: { runs: Record<string, PipelineRun> } }).state;
    // Simulate restart: lease already expired.
    void stale; void past;
    const recovered = await store.recoverExpiredLeases(new Date(Date.now() + 10 * 60_000));
    expect(recovered).toBe(1);
    const run = await store.getRun("r1");
    expect(run?.status).toBe("interrupted");
    const pipeline = await store.get("p1");
    expect(pipeline?.lastStatus).toBe("interrupted");
  });
});
