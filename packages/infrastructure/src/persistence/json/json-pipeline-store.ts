import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import type { PipelineStorePort, Pipeline } from "@nusashell/application";

interface PersistedState {
  readonly pipelines: Record<string, Pipeline>;
}

const STATE_FILE = "pipelines.json";

/**
 * JSON sidecar PipelineStore for dev environments without SQLite.
 * Mirrors JsonJobStore's atomic write pattern (staging file + rename).
 */
export class JsonPipelineStore implements PipelineStorePort {
  private state: PersistedState = { pipelines: {} };
  private loaded = false;

  constructor(private readonly root: string) {}

  private async load(): Promise<void> {
    if (this.loaded) return;
    try {
      const data = await readFile(resolve(this.root, STATE_FILE), "utf8");
      const persisted = JSON.parse(data) as Partial<PersistedState>;
      this.state = { pipelines: persisted.pipelines ?? {} };
    } catch {
      this.state = { pipelines: {} };
    }
    this.loaded = true;
  }

  private async save(): Promise<void> {
    await mkdir(this.root, { recursive: true });
    const data = JSON.stringify(this.state, null, 2);
    const staging = resolve(this.root, `${STATE_FILE}.tmp`);
    const target = resolve(this.root, STATE_FILE);
    await writeFile(staging, data, "utf8");
    await rename(staging, target);
  }

  async create(pipeline: Pipeline): Promise<Pipeline> {
    await this.load();
    this.state = { pipelines: { ...this.state.pipelines, [pipeline.id]: pipeline } };
    await this.save();
    return pipeline;
  }

  async update(pipeline: Pipeline): Promise<Pipeline> {
    await this.load();
    this.state = { pipelines: { ...this.state.pipelines, [pipeline.id]: pipeline } };
    await this.save();
    return pipeline;
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
    await this.load();
    const { [id]: _, ...rest } = this.state.pipelines;
    this.state = { pipelines: rest };
    await this.save();
  }

  async markRun(
    id: string,
    status: "ok" | "error" | "cancelled",
    error: string | null,
    now: Date,
  ): Promise<Pipeline | null> {
    await this.load();
    const existing = this.state.pipelines[id];
    if (!existing) return null;
    const updated: Pipeline = {
      ...existing,
      lastRunAt: now.toISOString(),
      lastStatus: status,
      lastError: error,
    };
    this.state = { pipelines: { ...this.state.pipelines, [id]: updated } };
    await this.save();
    return updated;
  }
}
