import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { randomUUID } from "node:crypto";
import { resolve } from "node:path";
import type { PipelineStorePort } from "@nusashell/application";
import type {
  Pipeline,
  PipelineRun,
  PipelineStatus,
} from "@nusashell/application";
import { isTerminalPipelineRunStatus } from "@nusashell/application";

interface PersistedState {
  readonly pipelines: Record<string, Pipeline>;
  readonly runs: Record<string, PipelineRun>;
  /** pipelineId → active runId while non-terminal. */
  readonly activeByPipeline: Record<string, string>;
}

const STATE_FILE = "pipelines.json";
const DEFAULT_RUN_HISTORY = 50;

/**
 * JSON sidecar PipelineStore for local desktop.
 * All mutations serialize on a write queue; claim is max-1 per pipeline.
 */
export class JsonPipelineStore implements PipelineStorePort {
  private state: PersistedState = { pipelines: {}, runs: {}, activeByPipeline: {} };
  private loaded = false;
  private writeChain: Promise<void> = Promise.resolve();

  constructor(private readonly root: string) {}

  private async load(): Promise<void> {
    if (this.loaded) return;
    try {
      const data = await readFile(resolve(this.root, STATE_FILE), "utf8");
      const persisted = JSON.parse(data) as Partial<PersistedState>;
      const pipelines: Record<string, Pipeline> = {};
      for (const [id, raw] of Object.entries(persisted.pipelines ?? {})) {
        const p = raw as Pipeline;
        pipelines[id] = {
          ...p,
          nextRunAt: p.nextRunAt ?? null,
        };
      }
      this.state = {
        pipelines,
        runs: persisted.runs ?? {},
        activeByPipeline: persisted.activeByPipeline ?? {},
      };
    } catch {
      this.state = { pipelines: {}, runs: {}, activeByPipeline: {} };
    }
    this.loaded = true;
  }

  private async save(): Promise<void> {
    await mkdir(this.root, { recursive: true });
    const data = JSON.stringify(this.state, null, 2);
    const staging = resolve(this.root, `${STATE_FILE}.${randomUUID()}.tmp`);
    const target = resolve(this.root, STATE_FILE);
    await writeFile(staging, data, "utf8");
    await rename(staging, target);
  }

  private enqueue<T>(fn: () => Promise<T>): Promise<T> {
    const run = this.writeChain.then(fn, fn);
    this.writeChain = run.then(
      () => undefined,
      () => undefined,
    );
    return run;
  }

  async create(pipeline: Pipeline): Promise<Pipeline> {
    return this.enqueue(async () => {
      await this.load();
      this.state = {
        ...this.state,
        pipelines: { ...this.state.pipelines, [pipeline.id]: pipeline },
      };
      await this.save();
      return pipeline;
    });
  }

  async update(pipeline: Pipeline): Promise<Pipeline> {
    return this.enqueue(async () => {
      await this.load();
      this.state = {
        ...this.state,
        pipelines: { ...this.state.pipelines, [pipeline.id]: pipeline },
      };
      await this.save();
      return pipeline;
    });
  }

  async get(id: string): Promise<Pipeline | null> {
    await this.load();
    return this.state.pipelines[id] ?? null;
  }

  async list(): Promise<readonly Pipeline[]> {
    await this.load();
    return Object.values(this.state.pipelines);
  }

  async remove(id: string): Promise<void> {
    return this.enqueue(async () => {
      await this.load();
      const { [id]: _, ...rest } = this.state.pipelines;
      const { [id]: _active, ...activeRest } = this.state.activeByPipeline;
      // Drop orphaned run history for the removed pipeline so pipelines.json
      // doesn't retain runs for deleted definitions.
      const runs: Record<string, PipelineRun> = {};
      for (const [runId, run] of Object.entries(this.state.runs)) {
        if (run.pipelineId !== id) runs[runId] = run;
      }
      this.state = {
        pipelines: rest,
        runs,
        activeByPipeline: activeRest,
      };
      await this.save();
    });
  }

  async claimRun(run: PipelineRun): Promise<PipelineRun | null> {
    return this.enqueue(async () => {
      await this.load();
      const pipeline = this.state.pipelines[run.pipelineId];
      if (!pipeline) return null;
      const existingId = this.state.activeByPipeline[run.pipelineId];
      if (existingId) {
        const existing = this.state.runs[existingId];
        if (existing && !isTerminalPipelineRunStatus(existing.status)) {
          return null;
        }
      }
      const claimed: PipelineRun = { ...run, status: "claimed" };
      const updatedPipeline: Pipeline = {
        ...pipeline,
        lastStatus: "running",
        lastRunAt: claimed.startedAt,
        lastError: null,
        lastRunId: claimed.runId,
      };
      this.state = {
        pipelines: { ...this.state.pipelines, [pipeline.id]: updatedPipeline },
        runs: { ...this.state.runs, [claimed.runId]: claimed },
        activeByPipeline: { ...this.state.activeByPipeline, [pipeline.id]: claimed.runId },
      };
      this.pruneRuns(pipeline.id);
      await this.save();
      return claimed;
    });
  }

  async updateRun(run: PipelineRun): Promise<PipelineRun | null> {
    return this.enqueue(async () => {
      await this.load();
      const existing = this.state.runs[run.runId];
      if (!existing) return null;
      if (isTerminalPipelineRunStatus(existing.status)) return existing;
      this.state = {
        ...this.state,
        runs: { ...this.state.runs, [run.runId]: run },
      };
      await this.save();
      return run;
    });
  }

  async finalizeRun(
    run: PipelineRun,
    status: Extract<PipelineStatus, "ok" | "error" | "cancelled" | "interrupted">,
    error: string | null,
    now: Date,
  ): Promise<PipelineRun | null> {
    return this.enqueue(async () => {
      await this.load();
      const existing = this.state.runs[run.runId];
      if (!existing) return null;
      if (isTerminalPipelineRunStatus(existing.status)) {
        return existing;
      }
      const completedAt = now.toISOString();
      const finalRun: PipelineRun = {
        ...run,
        status,
        completedAt,
        lastHeartbeatAt: completedAt,
        errorCode: run.errorCode,
        errorMessage: error,
      };
      const pipeline = this.state.pipelines[run.pipelineId];
      const { [run.pipelineId]: _active, ...activeRest } = this.state.activeByPipeline;
      const updatedPipeline = pipeline
        ? {
            ...pipeline,
            lastRunAt: completedAt,
            lastStatus: status,
            lastError: error,
            lastRunId: run.runId,
          }
        : undefined;
      this.state = {
        pipelines: updatedPipeline
          ? { ...this.state.pipelines, [run.pipelineId]: updatedPipeline }
          : this.state.pipelines,
        runs: { ...this.state.runs, [run.runId]: finalRun },
        activeByPipeline: activeRest,
      };
      await this.save();
      return finalRun;
    });
  }

  async getRun(runId: string): Promise<PipelineRun | null> {
    await this.load();
    return this.state.runs[runId] ?? null;
  }

  async getActiveRun(pipelineId: string): Promise<PipelineRun | null> {
    await this.load();
    const runId = this.state.activeByPipeline[pipelineId];
    if (!runId) return null;
    const run = this.state.runs[runId];
    if (!run || isTerminalPipelineRunStatus(run.status)) return null;
    return run;
  }

  async listRuns(pipelineId: string, limit = 20): Promise<readonly PipelineRun[]> {
    await this.load();
    return Object.values(this.state.runs)
      .filter((run) => run.pipelineId === pipelineId)
      .sort((a, b) => b.startedAt.localeCompare(a.startedAt))
      .slice(0, limit);
  }

  async listDueSchedules(now: Date): Promise<readonly Pipeline[]> {
    await this.load();
    const nowIso = now.toISOString();
    return Object.values(this.state.pipelines).filter((pipeline) => {
      if (!pipeline.enabled) return false;
      if (pipeline.trigger.kind !== "schedule") return false;
      if (!pipeline.nextRunAt || pipeline.nextRunAt > nowIso) return false;
      const activeId = this.state.activeByPipeline[pipeline.id];
      if (activeId) {
        const active = this.state.runs[activeId];
        if (active && !isTerminalPipelineRunStatus(active.status)) return false;
      }
      return true;
    });
  }

  async recoverExpiredLeases(now: Date): Promise<number> {
    return this.enqueue(async () => {
      await this.load();
      const nowMs = now.getTime();
      let recovered = 0;
      const runs = { ...this.state.runs };
      const activeByPipeline = { ...this.state.activeByPipeline };
      const pipelines = { ...this.state.pipelines };

      for (const [pipelineId, runId] of Object.entries(activeByPipeline)) {
        const run = runs[runId];
        if (!run || isTerminalPipelineRunStatus(run.status)) {
          delete activeByPipeline[pipelineId];
          continue;
        }
        const leaseMs = Date.parse(run.leaseExpiresAt);
        if (!Number.isFinite(leaseMs) || leaseMs > nowMs) continue;
        const completedAt = now.toISOString();
        runs[runId] = {
          ...run,
          status: "interrupted",
          completedAt,
          lastHeartbeatAt: completedAt,
          errorCode: "PIPELINE_INTERRUPTED",
          errorMessage: "Run interrupted (lease expired or process restarted)",
        };
        const pipeline = pipelines[pipelineId];
        if (pipeline) {
          pipelines[pipelineId] = {
            ...pipeline,
            lastRunAt: completedAt,
            lastStatus: "interrupted",
            lastError: runs[runId]!.errorMessage,
            lastRunId: runId,
          };
        }
        delete activeByPipeline[pipelineId];
        recovered += 1;
      }

      if (recovered > 0) {
        this.state = { pipelines, runs, activeByPipeline };
        await this.save();
      }
      return recovered;
    });
  }

  async markRun(
    id: string,
    status: "ok" | "error" | "cancelled",
    error: string | null,
    now: Date,
  ): Promise<Pipeline | null> {
    return this.enqueue(async () => {
      await this.load();
      const existing = this.state.pipelines[id];
      if (!existing) return null;
      const updated: Pipeline = {
        ...existing,
        lastRunAt: now.toISOString(),
        lastStatus: status,
        lastError: error,
      };
      this.state = {
        ...this.state,
        pipelines: { ...this.state.pipelines, [id]: updated },
      };
      await this.save();
      return updated;
    });
  }

  private pruneRuns(pipelineId: string): void {
    const forPipeline = Object.values(this.state.runs)
      .filter((run) => run.pipelineId === pipelineId)
      .sort((a, b) => b.startedAt.localeCompare(a.startedAt));
    if (forPipeline.length <= DEFAULT_RUN_HISTORY) return;
    const keep = new Set(forPipeline.slice(0, DEFAULT_RUN_HISTORY).map((r) => r.runId));
    const runs: Record<string, PipelineRun> = {};
    for (const [id, run] of Object.entries(this.state.runs)) {
      if (run.pipelineId !== pipelineId || keep.has(id)) runs[id] = run;
    }
    this.state = { ...this.state, runs };
  }
}
