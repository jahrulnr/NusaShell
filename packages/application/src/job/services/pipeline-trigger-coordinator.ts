/**
 * Wall-clock schedule coordinator for pipelines.
 * Reuses job schedule grammar (once/interval/cron) and grace windows.
 * Fires through PipelineScheduler claims; max concurrency remains 1 per pipeline.
 */

import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import type { EventDispatcher } from "../../events/event-dispatcher.js";
import {
  createPipelineFailedEvent,
} from "../../events/pipeline-events.event.js";
import type { JobSchedule } from "../job-model.js";
import {
  ONCE_GRACE_SECONDS,
  isRecurring,
  recurringCatchupGraceSeconds,
} from "../job-model.js";
import {
  nextRunAtForPipelineTrigger,
  type Pipeline,
} from "../pipeline-model.js";
import type { PipelineStorePort } from "../ports/pipeline-store.port.js";
import type { PipelineScheduler } from "./pipeline-scheduler.js";

export interface PipelineTriggerCoordinatorSettings {
  readonly enabled: boolean;
  readonly tickSeconds: number;
}

export const DEFAULT_PIPELINE_TRIGGER_SETTINGS: PipelineTriggerCoordinatorSettings = {
  enabled: true,
  tickSeconds: 15,
};

export interface PipelineTriggerCoordinatorDeps {
  readonly store: PipelineStorePort;
  readonly scheduler: PipelineScheduler;
  readonly eventDispatcher: EventDispatcher;
  readonly logger?: LoggerPort;
  readonly now?: () => Date;
}

export class PipelineTriggerCoordinator {
  private settings: PipelineTriggerCoordinatorSettings = { ...DEFAULT_PIPELINE_TRIGGER_SETTINGS };
  private timer: ReturnType<typeof setInterval> | null = null;
  private ticking = false;

  constructor(private readonly deps: PipelineTriggerCoordinatorDeps) {}

  configure(settings: Partial<PipelineTriggerCoordinatorSettings>): void {
    this.settings = { ...this.settings, ...settings };
  }

  getSettings(): PipelineTriggerCoordinatorSettings {
    return this.settings;
  }

  start(): void {
    if (this.timer) return;
    if (!this.settings.enabled) {
      this.deps.logger?.info("pipeline schedule coordinator disabled; not starting");
      return;
    }
    void this.tick().catch((err) => {
      this.deps.logger?.error(
        "pipeline schedule initial tick failed: %s",
        err instanceof Error ? err.message : String(err),
      );
    });
    this.timer = setInterval(() => {
      void this.tick().catch((err) => {
        this.deps.logger?.error(
          "pipeline schedule tick failed: %s",
          err instanceof Error ? err.message : String(err),
        );
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
      const now = (this.deps.now ?? (() => new Date()))();
      const due = await this.deps.store.listDueSchedules(now);
      for (const pipeline of due) {
        await this.processDue(pipeline, now);
      }
    } catch (error) {
      this.deps.logger?.error(
        "pipeline schedule tick error: %s",
        error instanceof Error ? error.message : String(error),
      );
    } finally {
      this.ticking = false;
    }
  }

  private async processDue(pipeline: Pipeline, now: Date): Promise<void> {
    if (pipeline.trigger.kind !== "schedule") return;
    const schedule = pipeline.trigger.schedule;

    if (schedule.kind === "once") {
      const runAtMs = new Date(schedule.runAt).getTime();
      const ageSeconds = (now.getTime() - runAtMs) / 1000;
      if (ageSeconds > ONCE_GRACE_SECONDS) {
        this.deps.logger?.warn("pipeline %s missed while app was closed; marking error", pipeline.id);
        await this.markMissed(pipeline, now, "missed while app was closed");
        return;
      }
    }

    if (isRecurring(schedule)) {
      const periodMinutes = periodMinutesFor(schedule);
      const grace = recurringCatchupGraceSeconds(periodMinutes);
      const nextRunMs = pipeline.nextRunAt ? new Date(pipeline.nextRunAt).getTime() : now.getTime();
      const latenessSeconds = (now.getTime() - nextRunMs) / 1000;
      if (latenessSeconds > grace) {
        this.deps.logger?.info("pipeline %s catchup: firing once and fast-forwarding", pipeline.id);
      }
    }

    try {
      const outcome = await this.deps.scheduler.runPipeline(pipeline.id, undefined, { source: "schedule" });
      if (!outcome.ok && outcome.errorCode === "PIPELINE_ALREADY_RUNNING") {
        this.deps.logger?.debug("pipeline %s schedule tick skipped: already running", pipeline.id);
        return;
      }
      if (!outcome.ok) {
        this.deps.logger?.error(
          "pipeline %s schedule fire failed: %s",
          pipeline.id,
          outcome.error ?? "unknown",
        );
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      this.deps.logger?.error("pipeline %s schedule dispatch error: %s", pipeline.id, message);
    }
  }

  private async markMissed(pipeline: Pipeline, now: Date, error: string): Promise<void> {
    const nextRunAt = nextRunAtForPipelineTrigger(pipeline.trigger, now.toISOString(), now, pipeline.enabled);
    await this.deps.store.update({
      ...pipeline,
      nextRunAt,
      lastRunAt: now.toISOString(),
      lastStatus: "error",
      lastError: error,
    });
    await this.deps.eventDispatcher.publish(
      createPipelineFailedEvent(
        pipeline.id,
        pipeline.name,
        "missed",
        "missed",
        error,
        now,
        "PIPELINE_SCHEDULE_MISSED",
      ),
    );
  }
}

function periodMinutesFor(schedule: JobSchedule): number {
  if (schedule.kind === "interval") return schedule.minutes;
  return 60;
}
