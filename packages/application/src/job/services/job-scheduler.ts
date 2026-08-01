import { randomUUID } from "node:crypto";
import type { JobStorePort } from "../ports/job-store.port.js";
import type { JobFsPort } from "../ports/job-fs.port.js";
import type { Job, JobOutputEntry } from "../job-model.js";
import {
  computeNextRun,
  describeSchedule,
} from "../schedule-parser.js";
import {
  ONCE_GRACE_SECONDS,
  recurringCatchupGraceSeconds,
  isRecurring,
} from "../job-model.js";
import type { JobAgentExecutorSettings, JobExecutionResult } from "./job-agent-executor.js";
import type { CallToolCommand } from "../../tool/commands/call-tool/call-tool.command.js";
import type { EventDispatcher } from "../../events/event-dispatcher.js";
import { createJobCompletedEvent, createJobFailedEvent, createJobStartedEvent, createJobCancelledEvent } from "../../events/job-events.event.js";
import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import type { ReasoningEffort } from "../../agent/ports/agent-provider.port.js";

/** Minimal executor surface the scheduler needs (structural — accepts JobAgentExecutor or fakes). */
export interface JobExecutorPort {
  runAgent(
    prompt: string,
    settings: JobAgentExecutorSettings,
    signal?: AbortSignal,
    options?: { providerId?: string; model?: string; effort?: ReasoningEffort },
  ): Promise<JobExecutionResult>;
}

/** Minimal call-tool surface the scheduler needs (structural — accepts CallToolHandler or fakes). */
export interface JobCallToolPort {
  handle(command: CallToolCommand): Promise<{ requestId: string; result: unknown }>;
}

export interface JobSchedulerSettings {
  readonly enabled: boolean;
  readonly tickSeconds: number;
  readonly inactivityTimeoutSeconds: number;
  readonly maxOutputChars: number;
  readonly claimTtlSeconds: number;
}

export const DEFAULT_JOB_SCHEDULER_SETTINGS: JobSchedulerSettings = {
  enabled: true,
  tickSeconds: 60,
  inactivityTimeoutSeconds: 600,
  maxOutputChars: 8000,
  claimTtlSeconds: 300,
};

export interface JobSchedulerDeps {
  readonly store: JobStorePort;
  readonly executor: JobExecutorPort;
  readonly callToolHandler: JobCallToolPort;
  readonly eventDispatcher: EventDispatcher;
  readonly jobFs: JobFsPort;
  readonly executorSettings: JobAgentExecutorSettings;
  readonly logger?: LoggerPort;
  readonly now?: () => Date;
}

interface ActiveRun {
  readonly traceId: string;
  readonly controller: AbortController;
  readonly startedAt: Date;
}

/**
 * Wall-clock tick scheduler for durable jobs. Runs a 60s interval plus one
 * immediate tick at start. Each tick acquires an exclusive `.tick.lock`,
 * selects due jobs, claims each (at-most-once), dispatches them sequentially,
 * persists output, and publishes completion/failure events.
 *
 * Jobs run only while NusaShell is open. Missed one-shots (past the 120s grace
 * at tick time, e.g. after an app restart) are marked errored + disabled
 * rather than silently dropped.
 */
export class JobScheduler {
  private settings: JobSchedulerSettings = DEFAULT_JOB_SCHEDULER_SETTINGS;
  private timer: ReturnType<typeof setInterval> | null = null;
  private ticking = false;
  private readonly activeJobIds = new Set<string>();
  private readonly activeRuns = new Map<string, ActiveRun>();
  private lastTickAt: string | null = null;

  constructor(private readonly deps: JobSchedulerDeps) {}

  configure(settings: Partial<JobSchedulerSettings>): void {
    this.settings = { ...this.settings, ...settings };
  }

  getSettings(): JobSchedulerSettings {
    return this.settings;
  }

  getStatus(): { running: boolean; lastTickAt: string | null; activeJobIds: readonly string[] } {
    return {
      running: this.timer !== null,
      lastTickAt: this.lastTickAt,
      activeJobIds: [...this.activeJobIds],
    };
  }

  /** Whether a job is currently in-flight (dispatched but not yet completed). */
  isRunning(jobId: string): boolean {
    return this.activeRuns.has(jobId);
  }

  /** The traceId of the currently active run for a job, or null. */
  activeTraceId(jobId: string): string | null {
    return this.activeRuns.get(jobId)?.traceId ?? null;
  }

  /**
   * Cancel an in-flight job run. Aborts the AbortController, marks the job
   * as cancelled, publishes job.cancelled, and releases the claim.
   * Returns false if the job is not currently running.
   */
  async cancel(jobId: string): Promise<{ ok: boolean; error?: string }> {
    const run = this.activeRuns.get(jobId);
    if (!run) return { ok: false, error: "job is not running" };
    run.controller.abort();
    // The dispatch method's finally block will clean up the activeRuns entry
    // and activeJobIds. We publish the cancelled event here; the dispatch
    // method will still persist output + markRun + publish completed/failed
    // depending on how the executor exits.
    const job = await this.deps.store.get(jobId);
    const name = job?.name ?? jobId;
    await this.deps.eventDispatcher.publish(createJobCancelledEvent(jobId, name, run.traceId));
    return { ok: true };
  }

  start(): void {
    if (this.timer) return;
    if (!this.settings.enabled) {
      this.deps.logger?.info("job scheduler disabled; not starting");
      return;
    }
    // One immediate tick so due recurring jobs catch up at launch.
    void this.tick().catch((err) => {
      this.deps.logger?.error("job scheduler initial tick failed: %s", err instanceof Error ? err.message : String(err));
    });
    this.timer = setInterval(() => {
      void this.tick().catch((err) => {
        this.deps.logger?.error("job scheduler tick failed: %s", err instanceof Error ? err.message : String(err));
      });
    }, Math.max(10, this.settings.tickSeconds) * 1000);
  }

  stop(): void {
    if (this.timer) {
      clearInterval(this.timer);
      this.timer = null;
    }
  }

  async tick(): Promise<void> {
    if (!this.settings.enabled) return;
    if (this.ticking) return;
    this.ticking = true;
    try {
      if (!(await this.acquireTickLock())) {
        this.deps.logger?.debug("job scheduler tick skipped: lock held");
        return;
      }
      const now = (this.deps.now ?? (() => new Date()))();
      this.lastTickAt = now.toISOString();
      const due = await this.deps.store.listDue(now);
      for (const job of due) {
        await this.processJob(job, now);
      }
    } catch (error) {
      this.deps.logger?.error("job scheduler tick error: %s", error instanceof Error ? error.message : String(error));
    } finally {
      await this.releaseTickLock();
      this.ticking = false;
    }
  }

  /** Fire a job immediately (manual run), respecting the claim lock. */
  async runOneNow(jobId: string): Promise<{ ok: boolean; error?: string }> {
    const job = await this.deps.store.get(jobId);
    if (!job) return { ok: false, error: "job not found" };
    if (!job.enabled) return { ok: false, error: "job is paused" };
    const now = (this.deps.now ?? (() => new Date()))();
    const claimId = randomUUID();
    const claimed = await this.deps.store.claimFire(jobId, claimId, this.settings.claimTtlSeconds, now);
    if (!claimed) return { ok: false, error: "job is already running" };
    try {
      await this.dispatch(job, now, claimId);
      return { ok: true };
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      return { ok: false, error: message };
    }
  }

  private async processJob(job: Job, now: Date): Promise<void> {
    const claimId = randomUUID();
    const claimed = await this.deps.store.claimFire(job.id, claimId, this.settings.claimTtlSeconds, now);
    if (!claimed) return;

    // Missed one-shot: runAt older than the grace window at tick time.
    if (job.schedule.kind === "once") {
      const runAtMs = new Date(job.schedule.runAt).getTime();
      const ageSeconds = (now.getTime() - runAtMs) / 1000;
      if (ageSeconds > ONCE_GRACE_SECONDS) {
        this.deps.logger?.warn("job %s missed while app was closed; marking error", job.id);
        await this.deps.store.markRun(job.id, "error", "missed while app was closed", null, now);
        await this.deps.store.releaseFire(job.id, claimId);
        await this.deps.eventDispatcher.publish(
          createJobFailedEvent(job.id, job.name, "missed while app was closed", now),
        );
        return;
      }
    }

    // Catchup for recurring jobs late beyond the grace window: fire once now,
    // then fast-forward nextRunAt past the missed slots.
    if (isRecurring(job.schedule)) {
      const periodMinutes = periodMinutesFor(job.schedule);
      const grace = recurringCatchupGraceSeconds(periodMinutes);
      const nextRunMs = job.nextRunAt ? new Date(job.nextRunAt).getTime() : now.getTime();
      const latenessSeconds = (now.getTime() - nextRunMs) / 1000;
      if (latenessSeconds > grace) {
        this.deps.logger?.info("job %s catchup: firing once and fast-forwarding", job.id);
      }
    }

    try {
      await this.dispatch(job, now, claimId);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      this.deps.logger?.error("job %s dispatch failed: %s", job.id, message);
      await this.deps.store.markRun(job.id, "error", message, computeNext(job.schedule, now.toISOString(), now), now);
      await this.deps.store.releaseFire(job.id, claimId);
      await this.deps.eventDispatcher.publish(createJobFailedEvent(job.id, job.name, message, now));
    }
  }

  private async dispatch(job: Job, now: Date, claimId: string): Promise<void> {
    this.activeJobIds.add(job.id);
    const traceId = randomUUID();
    const controller = new AbortController();
    const run: ActiveRun = { traceId, controller, startedAt: now };
    this.activeRuns.set(job.id, run);
    await this.deps.eventDispatcher.publish(createJobStartedEvent(job.id, job.name, traceId, now, job.mode));
    try {
      let status: "ok" | "error" | "cancelled";
      let summary: string;
      let error: string | null = null;

      if (job.mode.type === "agent") {
        const result = await this.deps.executor.runAgent(
          job.mode.prompt,
          this.deps.executorSettings,
          controller.signal,
          {
            ...(job.mode.providerId ? { providerId: job.mode.providerId } : {}),
            ...(job.mode.model ? { model: job.mode.model } : {}),
            ...(job.mode.effort ? { effort: job.mode.effort } : {}),
          },
        );
        if (controller.signal.aborted) {
          status = "cancelled";
          summary = "cancelled by user";
          error = "cancelled by user";
        } else {
          status = result.status;
          summary = result.summary;
          if (result.status === "error") error = result.error ?? summary;
        }
      } else {
        const command: CallToolCommand = {
          kind: "call-tool",
          pluginId: job.mode.pluginId,
          requestId: randomUUID(),
          toolName: job.mode.toolName,
          args: job.mode.args,
        };
        try {
          const result = await this.deps.callToolHandler.handle(command);
          summary = formatToolOutput(result.result, this.settings.maxOutputChars);
          status = controller.signal.aborted ? "cancelled" : "ok";
          if (status === "cancelled") { summary = "cancelled by user"; error = "cancelled by user"; }
        } catch (err) {
          error = err instanceof Error ? err.message : String(err);
          summary = error;
          status = "error";
        }
      }

      const outputEntry = await this.persistOutput(job, now, status, summary, traceId);
      if (outputEntry) await this.deps.store.appendOutput(job.id, outputEntry);
      const nextRunAt = computeNext(job.schedule, now.toISOString(), now);
      await this.deps.store.markRun(job.id, status, error, nextRunAt, now);
      await this.deps.store.releaseFire(job.id, claimId);

      if (status === "ok") {
        await this.deps.eventDispatcher.publish(createJobCompletedEvent(job.id, job.name, summary, now, traceId));
      } else if (status === "cancelled") {
        // job.cancelled already published by cancel(); publish failed for consistency
        await this.deps.eventDispatcher.publish(createJobFailedEvent(job.id, job.name, error ?? summary, now, traceId));
      } else {
        await this.deps.eventDispatcher.publish(createJobFailedEvent(job.id, job.name, error ?? summary, now, traceId));
      }
    } finally {
      this.activeJobIds.delete(job.id);
      this.activeRuns.delete(job.id);
    }
  }

  private async persistOutput(
    job: Job,
    now: Date,
    status: "ok" | "error" | "cancelled",
    summary: string,
    traceId: string,
  ): Promise<JobOutputEntry | null> {
    try {
      const stamp = now.toISOString().replace(/[:.]/g, "-");
      const header = `# Job: ${job.name}\n- id: ${job.id}\n- schedule: ${describeSchedule(job.schedule)}\n- runAt: ${now.toISOString()}\n- status: ${status}\n- traceId: ${traceId}\n\n`;
      const content = header + summary + "\n";
      const path = await this.deps.jobFs.persistJobOutput(job.id, stamp, content);
      if (path === null) return null;
      return {
        jobId: job.id,
        runAt: now.toISOString(),
        status,
        summary: summary.slice(0, 500),
        path,
        traceId,
      };
    } catch (error) {
      this.deps.logger?.warn("job %s output persist failed: %s", job.id, error instanceof Error ? error.message : String(error));
      return null;
    }
  }

  private async acquireTickLock(): Promise<boolean> {
    return this.deps.jobFs.acquireTickLock();
  }

  private async releaseTickLock(): Promise<void> {
    await this.deps.jobFs.releaseTickLock();
  }
}

function periodMinutesFor(schedule: Job["schedule"]): number {
  if (schedule.kind === "interval") return schedule.minutes;
  // cron: approximate with a 1h minimum; exact period is not needed for grace.
  return 60;
}

function computeNext(
  schedule: Job["schedule"],
  lastRunAt: string | null,
  now: Date,
): string | null {
  return computeNextRun(schedule, lastRunAt, now);
}

function formatToolOutput(result: unknown, maxChars: number): string {
  let text: string;
  if (typeof result === "string") text = result;
  else {
    try {
      text = JSON.stringify(result, null, 2);
    } catch {
      text = String(result);
    }
  }
  return text.length <= maxChars ? text : `${text.slice(0, maxChars)}\n…[truncated]`;
}
