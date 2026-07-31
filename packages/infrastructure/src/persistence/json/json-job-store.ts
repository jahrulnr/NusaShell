import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { randomBytes } from "node:crypto";
import type { JobStorePort } from "@nusashell/application";
import type { Job, JobOutputEntry } from "@nusashell/application";

interface PersistedClaim {
  readonly claimId: string;
  readonly expiresAt: string;
}

interface PersistedState {
  readonly jobs: Record<string, Job>;
  readonly claims: Record<string, PersistedClaim>;
  readonly outputs: Record<string, JobOutputEntry[]>;
}

const STATE_FILE = "jobs.json";

/**
 * JSON sidecar JobStore for dev environments without SQLite
 * (`NUSASHELL_DB_PATH` unset). Mirrors the `SkillCuratorScheduler` atomic
 * write pattern (staging file + rename).
 */
export class JsonJobStore implements JobStorePort {
  private state: PersistedState = { jobs: {}, claims: {}, outputs: {} };
  private loaded = false;

  constructor(private readonly root: string) {}

  private async load(): Promise<void> {
    if (this.loaded) return;
    try {
      const data = await readFile(resolve(this.root, STATE_FILE), "utf8");
      this.state = JSON.parse(data) as PersistedState;
      // Defensive defaults for older states.
      this.state.claims ??= {};
      this.state.outputs ??= {};
    } catch {
      this.state = { jobs: {}, claims: {}, outputs: {} };
    }
    this.loaded = true;
  }

  private async persist(): Promise<void> {
    await mkdir(this.root, { recursive: true });
    const target = resolve(this.root, STATE_FILE);
    const staging = resolve(this.root, `.jobs-${randomBytes(8).toString("hex")}.json`);
    await writeFile(staging, JSON.stringify(this.state, null, 2), "utf8");
    await rename(staging, target);
  }

  async create(job: Job): Promise<Job> {
    await this.load();
    this.state = { ...this.state, jobs: { ...this.state.jobs, [job.id]: job } };
    await this.persist();
    return job;
  }

  async update(job: Job): Promise<Job> {
    await this.load();
    this.state = { ...this.state, jobs: { ...this.state.jobs, [job.id]: job } };
    await this.persist();
    return job;
  }

  async get(id: string): Promise<Job | null> {
    await this.load();
    return this.state.jobs[id] ?? null;
  }

  async list(): Promise<readonly Job[]> {
    await this.load();
    return Object.values(this.state.jobs).sort((a, b) =>
      b.createdAt.localeCompare(a.createdAt),
    );
  }

  async remove(id: string): Promise<void> {
    await this.load();
    const jobs = { ...this.state.jobs };
    delete jobs[id];
    const claims = { ...this.state.claims };
    delete claims[id];
    const outputs = { ...this.state.outputs };
    delete outputs[id];
    this.state = { jobs, claims, outputs };
    await this.persist();
  }

  async markRun(
    id: string,
    status: "ok" | "error",
    error: string | null,
    nextRunAt: string | null,
    now: Date,
  ): Promise<Job | null> {
    await this.load();
    const existing = this.state.jobs[id];
    if (!existing) return null;
    const completed = existing.repeat.completed + 1;
    const times = existing.repeat.times;
    const limitReached = times !== null && completed >= times;
    const enabled = existing.schedule.kind === "once"
      ? false
      : limitReached ? false : existing.enabled;
    const updated: Job = {
      ...existing,
      enabled,
      repeat: { ...existing.repeat, completed },
      nextRunAt,
      lastRunAt: now.toISOString(),
      lastStatus: status,
      lastError: error,
    };
    this.state = { ...this.state, jobs: { ...this.state.jobs, [id]: updated } };
    await this.persist();
    return updated;
  }

  async claimFire(jobId: string, claimId: string, ttlSeconds: number, now: Date): Promise<boolean> {
    await this.load();
    this.reapClaims(now);
    if (this.state.claims[jobId]) return false;
    const expiresAt = new Date(now.getTime() + ttlSeconds * 1000).toISOString();
    this.state = {
      ...this.state,
      claims: { ...this.state.claims, [jobId]: { claimId, expiresAt } },
    };
    await this.persist();
    return true;
  }

  async releaseFire(jobId: string, _claimId: string): Promise<void> {
    await this.load();
    const claims = { ...this.state.claims };
    delete claims[jobId];
    this.state = { ...this.state, claims };
    await this.persist();
  }

  async listDue(now: Date): Promise<readonly Job[]> {
    await this.load();
    this.reapClaims(now);
    const nowIso = now.toISOString();
    return Object.values(this.state.jobs)
      .filter(
        (job) =>
          job.enabled &&
          job.nextRunAt !== null &&
          job.nextRunAt <= nowIso &&
          !this.state.claims[job.id],
      )
      .sort((a, b) => (a.nextRunAt ?? "").localeCompare(b.nextRunAt ?? ""));
  }

  async appendOutput(jobId: string, entry: JobOutputEntry): Promise<void> {
    await this.load();
    const existing = this.state.outputs[jobId] ?? [];
    this.state = {
      ...this.state,
      outputs: { ...this.state.outputs, [jobId]: [entry, ...existing].slice(0, 100) },
    };
    await this.persist();
  }

  async listOutputs(jobId: string, limit: number): Promise<readonly JobOutputEntry[]> {
    await this.load();
    return (this.state.outputs[jobId] ?? []).slice(0, Math.max(1, Math.min(limit, 100)));
  }

  private reapClaims(now: Date): void {
    const nowIso = now.toISOString();
    const claims: Record<string, PersistedClaim> = {};
    for (const [jobId, claim] of Object.entries(this.state.claims)) {
      if (claim.expiresAt > nowIso) claims[jobId] = claim;
    }
    this.state = { ...this.state, claims };
  }
}
