/**
 * PipelineScheduler — executes a claimed pipeline run step-by-step.
 * Max concurrency is 1 per pipeline (store claim). Supports cancel +
 * pipeline-level timeout. Schedule fires are admitted by PipelineTriggerCoordinator.
 */

import { randomUUID } from "node:crypto";
import type { EventDispatcher } from "../../events/event-dispatcher.js";
import {
  createPipelineStartedEvent,
  createPipelineCompletedEvent,
  createPipelineFailedEvent,
  createPipelineCancelledEvent,
  createPipelineStepUpdatedEvent,
} from "../../events/pipeline-events.event.js";
import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import type { ReasoningEffort } from "../../agent/ports/agent-provider.port.js";
import type {
  Pipeline,
  PipelineStep,
  PipelineStepResult,
  PipelineRun,
  PipelineStepRun,
  PipelineTriggerSource,
} from "../pipeline-model.js";
import {
  topologicalSort,
  validatePipeline,
  nextRunAtForPipelineTrigger,
} from "../pipeline-model.js";
import {
  boundPipelineText,
  boundContextValue,
  DEFAULT_PIPELINE_SUMMARY_MAX_CHARS,
  DEFAULT_PIPELINE_OUTPUT_PREVIEW_MAX_CHARS,
} from "../pipeline-output.js";
import type { PipelineStorePort } from "../ports/pipeline-store.port.js";
import type { TemplateContext } from "./job-template-resolver.js";
import { resolveTemplates, resolveTemplatesInRecord } from "./job-template-resolver.js";
import { evaluateConditionNodeAgainstObject } from "./event-job-matcher.js";
import type { CallToolCommand } from "../../tool/commands/call-tool/call-tool.command.js";
import type { JobAgentExecutorSettings, JobExecutionResult } from "./job-agent-executor.js";

export interface PipelineExecutorPort {
  runAgent(
    prompt: string,
    settings: JobAgentExecutorSettings,
    signal?: AbortSignal,
    options?: { providerId?: string; model?: string; effort?: ReasoningEffort },
  ): Promise<JobExecutionResult>;
}

export interface PipelineCallToolPort {
  handle(command: CallToolCommand): Promise<{ requestId: string; result: unknown }>;
}

export interface PipelineSchedulerDeps {
  readonly store: PipelineStorePort;
  readonly executor: PipelineExecutorPort;
  readonly callToolHandler: PipelineCallToolPort;
  readonly eventDispatcher: EventDispatcher;
  readonly executorSettings: JobAgentExecutorSettings;
  readonly logger?: LoggerPort;
  readonly now?: () => Date;
  readonly leaseTtlMs?: number;
}

export interface PipelineRunOutcome {
  readonly ok: boolean;
  readonly runId?: string;
  readonly traceId?: string;
  readonly status?: string;
  readonly error?: string;
  readonly errorCode?: string;
}

export const DEFAULT_PIPELINE_LEASE_TTL_MS = 5 * 60 * 1000;

interface ActiveRun {
  readonly runId: string;
  readonly pipelineId: string;
  readonly traceId: string;
  readonly controller: AbortController;
  readonly timeoutHandle: ReturnType<typeof setTimeout> | null;
}

export class PipelineScheduler {
  private readonly activeRuns = new Map<string, ActiveRun>(); // runId
  private readonly activeByPipeline = new Map<string, string>(); // pipelineId → runId

  constructor(private readonly deps: PipelineSchedulerDeps) {}

  private now(): Date {
    return (this.deps.now ?? (() => new Date()))();
  }

  private leaseTtlMs(): number {
    return this.deps.leaseTtlMs ?? DEFAULT_PIPELINE_LEASE_TTL_MS;
  }

  /** Recover interrupted runs from a previous process. */
  async recoverOnStartup(): Promise<number> {
    const recovered = await this.deps.store.recoverExpiredLeases(this.now());
    if (recovered > 0) {
      this.deps.logger?.info("pipeline recovery: marked %d interrupted run(s)", recovered);
    }
    return recovered;
  }

  isRunning(pipelineId: string): boolean {
    return this.activeByPipeline.has(pipelineId);
  }

  activeRunId(pipelineId: string): string | null {
    return this.activeByPipeline.get(pipelineId) ?? null;
  }

  /**
   * Launch a pipeline run in the background and return immediately with the
   * runId/traceId. The run executes asynchronously; progress is published via
   * pipeline.* events. `runPipeline` keeps the blocking variant for internal
   * callers (event/schedule coordinators) that already fire-and-track.
   */
  async launch(
    pipelineId: string,
    templateContext?: TemplateContext,
    options?: { readonly source?: PipelineTriggerSource },
  ): Promise<PipelineRunOutcome> {
    const pipeline = await this.deps.store.get(pipelineId);
    if (!pipeline) return { ok: false, error: "pipeline not found", errorCode: "PIPELINE_NOT_FOUND" };
    if (!pipeline.enabled) return { ok: false, error: "pipeline is disabled", errorCode: "PIPELINE_DISABLED" };

    const validationError = validatePipeline(pipeline.steps);
    if (validationError) return { ok: false, error: validationError, errorCode: "PIPELINE_INVALID" };

    const now = this.now();
    const runId = randomUUID();
    const traceId = randomUUID();
    const startedAt = now.toISOString();
    const leaseExpiresAt = new Date(now.getTime() + this.leaseTtlMs()).toISOString();
    const source = options?.source ?? "manual";

    const stepRuns: PipelineStepRun[] = pipeline.steps.map((step) => ({
      stepId: step.id,
      status: "queued" as const,
    }));

    const claim: PipelineRun = {
      runId,
      pipelineId: pipeline.id,
      traceId,
      status: "claimed",
      triggerSource: source,
      startedAt,
      completedAt: null,
      lastHeartbeatAt: startedAt,
      leaseExpiresAt,
      currentStepId: null,
      errorCode: null,
      errorMessage: null,
      stepRuns,
    };

    const claimed = await this.deps.store.claimRun(claim);
    if (!claimed) {
      return {
        ok: false,
        error: "pipeline is already running",
        errorCode: "PIPELINE_ALREADY_RUNNING",
      };
    }

    const controller = new AbortController();
    const timeoutMs = pipeline.settings?.timeoutMs;
    let timeoutHandle: ReturnType<typeof setTimeout> | null = null;
    if (typeof timeoutMs === "number" && timeoutMs > 0) {
      timeoutHandle = setTimeout(() => {
        controller.abort();
      }, timeoutMs);
    }

    this.activeRuns.set(runId, { runId, pipelineId: pipeline.id, traceId, controller, timeoutHandle });
    this.activeByPipeline.set(pipeline.id, runId);

    // Fire-and-track: execute in the background; the caller gets runId/traceId now.
    void this.executeRun(pipeline, claimed, templateContext, controller.signal, timeoutMs)
      .catch((error) => {
        this.deps.logger?.error(
          "pipeline %s background run error: %s",
          pipeline.id,
          error instanceof Error ? error.message : String(error),
        );
      })
      .finally(() => {
        if (timeoutHandle) clearTimeout(timeoutHandle);
        this.activeRuns.delete(runId);
        if (this.activeByPipeline.get(pipeline.id) === runId) {
          this.activeByPipeline.delete(pipeline.id);
        }
      });

    return { ok: true, runId, traceId, status: "claimed" };
  }
  /**
   * Cancel an in-flight run by runId (preferred) or pipelineId.
   */
  async cancel(runIdOrPipelineId: string): Promise<PipelineRunOutcome> {
    let active = this.activeRuns.get(runIdOrPipelineId);
    if (!active) {
      const byPipeline = this.activeByPipeline.get(runIdOrPipelineId);
      if (byPipeline) active = this.activeRuns.get(byPipeline);
    }
    if (!active) {
      return { ok: false, error: "pipeline is not running", errorCode: "PIPELINE_RUN_NOT_CANCELLABLE" };
    }
    active.controller.abort();
    const pipeline = await this.deps.store.get(active.pipelineId);
    await this.deps.eventDispatcher.publish(
      createPipelineCancelledEvent(
        active.pipelineId,
        pipeline?.name ?? active.pipelineId,
        active.runId,
        active.traceId,
      ),
    );
    return { ok: true, runId: active.runId, traceId: active.traceId, status: "cancelled" };
  }

  async runPipeline(
    pipelineId: string,
    templateContext?: TemplateContext,
    options?: { readonly source?: PipelineTriggerSource },
  ): Promise<PipelineRunOutcome> {
    const pipeline = await this.deps.store.get(pipelineId);
    if (!pipeline) return { ok: false, error: "pipeline not found", errorCode: "PIPELINE_NOT_FOUND" };
    if (!pipeline.enabled) return { ok: false, error: "pipeline is disabled", errorCode: "PIPELINE_DISABLED" };

    const validationError = validatePipeline(pipeline.steps);
    if (validationError) return { ok: false, error: validationError, errorCode: "PIPELINE_INVALID" };

    const now = this.now();
    const runId = randomUUID();
    const traceId = randomUUID();
    const startedAt = now.toISOString();
    const leaseExpiresAt = new Date(now.getTime() + this.leaseTtlMs()).toISOString();
    const source = options?.source ?? "manual";

    const stepRuns: PipelineStepRun[] = pipeline.steps.map((step) => ({
      stepId: step.id,
      status: "queued" as const,
    }));

    const claim: PipelineRun = {
      runId,
      pipelineId: pipeline.id,
      traceId,
      status: "claimed",
      triggerSource: source,
      startedAt,
      completedAt: null,
      lastHeartbeatAt: startedAt,
      leaseExpiresAt,
      currentStepId: null,
      errorCode: null,
      errorMessage: null,
      stepRuns,
    };

    const claimed = await this.deps.store.claimRun(claim);
    if (!claimed) {
      return {
        ok: false,
        error: "pipeline is already running",
        errorCode: "PIPELINE_ALREADY_RUNNING",
      };
    }

    const controller = new AbortController();
    const timeoutMs = pipeline.settings?.timeoutMs;
    let timeoutHandle: ReturnType<typeof setTimeout> | null = null;
    if (typeof timeoutMs === "number" && timeoutMs > 0) {
      timeoutHandle = setTimeout(() => {
        controller.abort();
      }, timeoutMs);
    }

    this.activeRuns.set(runId, { runId, pipelineId: pipeline.id, traceId, controller, timeoutHandle });
    this.activeByPipeline.set(pipeline.id, runId);

    try {
      return await this.executeRun(pipeline, claimed, templateContext, controller.signal, timeoutMs);
    } finally {
      if (timeoutHandle) clearTimeout(timeoutHandle);
      this.activeRuns.delete(runId);
      if (this.activeByPipeline.get(pipeline.id) === runId) {
        this.activeByPipeline.delete(pipeline.id);
      }
    }
  }

  private async executeRun(
    pipeline: Pipeline,
    claimed: PipelineRun,
    templateContext: TemplateContext | undefined,
    signal: AbortSignal,
    timeoutMs: number | undefined,
  ): Promise<PipelineRunOutcome> {
    const now0 = this.now();
    let run: PipelineRun = {
      ...claimed,
      status: "running",
      lastHeartbeatAt: now0.toISOString(),
      leaseExpiresAt: new Date(now0.getTime() + this.leaseTtlMs()).toISOString(),
    };
    await this.deps.store.updateRun(run);

    await this.deps.eventDispatcher.publish(
      createPipelineStartedEvent(
        pipeline.id,
        pipeline.name,
        claimed.runId,
        claimed.traceId,
        claimed.triggerSource,
        now0,
      ),
    );

    const context: Record<string, unknown> = {};
    const stepResults: PipelineStepResult[] = [];
    const sorted = topologicalSort(pipeline.steps);
    let pipelineStatus: "ok" | "error" | "cancelled" = "ok";
    let pipelineError: string | null = null;
    let errorCode: string | null = null;

    try {
      for (const step of sorted) {
        if (signal.aborted) {
          const aborted = this.abortOutcome(timeoutMs, claimed.startedAt);
          pipelineStatus = aborted.status;
          pipelineError = aborted.error;
          errorCode = aborted.errorCode;
          break;
        }

        const stepStart = this.now();
        run = await this.touchStep(run, step.id, "running", stepStart);
        await this.deps.eventDispatcher.publish(
          createPipelineStepUpdatedEvent(
            pipeline.id,
            claimed.runId,
            claimed.traceId,
            step.id,
            "running",
            "running",
            stepStart,
          ),
        );

        const stepTemplateCtx = templateContext
          ? { ...templateContext, context: { ...context } }
          : { event: { type: "", pluginId: "", payload: {} }, context: { ...context } };

        const stepResult = await this.runStep(step, context, stepTemplateCtx, pipeline, signal);
        const completedAt = this.now();
        const fullResult: PipelineStepResult = {
          ...stepResult,
          startedAt: stepStart.toISOString(),
          completedAt: completedAt.toISOString(),
        };
        stepResults.push(fullResult);

        const summaryBound = boundPipelineText(stepResult.summary, DEFAULT_PIPELINE_SUMMARY_MAX_CHARS);
        const errorBound = stepResult.error
          ? boundPipelineText(stepResult.error, DEFAULT_PIPELINE_SUMMARY_MAX_CHARS)
          : null;
        let outputPreview: string | undefined;
        let outputTruncated = summaryBound.truncated || (errorBound?.truncated ?? false);
        if (stepResult.output !== undefined && stepResult.status === "ok") {
          const preview = boundPipelineText(
            stepResult.output,
            DEFAULT_PIPELINE_OUTPUT_PREVIEW_MAX_CHARS,
          );
          outputPreview = preview.text;
          outputTruncated = outputTruncated || preview.truncated;
        }

        run = {
          ...run,
          lastHeartbeatAt: completedAt.toISOString(),
          leaseExpiresAt: new Date(completedAt.getTime() + this.leaseTtlMs()).toISOString(),
          currentStepId: step.id,
          stepRuns: run.stepRuns.map((sr) =>
            sr.stepId === step.id
              ? {
                  stepId: step.id,
                  status: stepResult.status,
                  summary: summaryBound.text,
                  ...(errorBound ? { error: errorBound.text } : {}),
                  ...(outputPreview !== undefined ? { outputPreview } : {}),
                  ...(outputTruncated ? { outputTruncated: true } : {}),
                  startedAt: fullResult.startedAt,
                  completedAt: fullResult.completedAt,
                }
              : sr,
          ),
        };
        await this.deps.store.updateRun(run);
        await this.deps.eventDispatcher.publish(
          createPipelineStepUpdatedEvent(
            pipeline.id,
            claimed.runId,
            claimed.traceId,
            step.id,
            stepResult.status,
            "running",
            completedAt,
            summaryBound.text,
            errorBound?.text,
          ),
        );

        if (stepResult.status === "error") {
          pipelineStatus = "error";
          pipelineError = `Step "${step.id}" failed: ${errorBound?.text ?? summaryBound.text}`;
          errorCode = "PIPELINE_STEP_FAILED";
          break;
        }
        if (stepResult.status === "cancelled") {
          const aborted = this.abortOutcome(timeoutMs, claimed.startedAt);
          pipelineStatus = aborted.status;
          pipelineError = aborted.error;
          errorCode = aborted.errorCode;
          break;
        }
        if (stepResult.status === "ok" && step.outputKey && stepResult.output !== undefined) {
          context[step.outputKey] = boundContextValue(stepResult.output, summaryBound.text);
        }
      }    } catch (err) {
      if (signal.aborted) {
        const aborted = this.abortOutcome(timeoutMs, claimed.startedAt);
        pipelineStatus = aborted.status;
        pipelineError = aborted.error;
        errorCode = aborted.errorCode;
      } else {
        pipelineStatus = "error";
        pipelineError = err instanceof Error ? err.message : String(err);
      }
    }

    if (signal.aborted && pipelineStatus === "ok") {
      const aborted = this.abortOutcome(timeoutMs, claimed.startedAt);
      pipelineStatus = aborted.status;
      pipelineError = aborted.error;
      errorCode = aborted.errorCode;
    }

    const completedAt = this.now();
    const finalStatus: "ok" | "error" | "cancelled" =
      pipelineStatus === "cancelled"
        ? "cancelled"
        : pipelineStatus === "error" || errorCode === "PIPELINE_TIMEOUT"
          ? "error"
          : "ok";

    // Timeout surfaces as error with PIPELINE_TIMEOUT code.
    const storeStatus = errorCode === "PIPELINE_TIMEOUT" ? "error" : finalStatus;

    const finalRun: PipelineRun = {
      ...run,
      status: storeStatus,
      completedAt: completedAt.toISOString(),
      lastHeartbeatAt: completedAt.toISOString(),
      errorCode: errorCode ?? (storeStatus === "error" ? "PIPELINE_STEP_FAILED" : null),
      errorMessage: pipelineError,
    };

    await this.deps.store.finalizeRun(finalRun, storeStatus, pipelineError, completedAt);
    await this.advanceNextRunAt(pipeline, completedAt);

    if (finalRun.status === "ok") {
      await this.deps.eventDispatcher.publish(
        createPipelineCompletedEvent(
          pipeline.id,
          pipeline.name,
          claimed.runId,
          claimed.traceId,
          `Pipeline completed (${stepResults.length} steps)`,
          completedAt,
        ),
      );
    } else if (finalRun.status !== "cancelled") {
      await this.deps.eventDispatcher.publish(
        createPipelineFailedEvent(
          pipeline.id,
          pipeline.name,
          claimed.runId,
          claimed.traceId,
          pipelineError ?? "unknown",
          completedAt,
          finalRun.errorCode ?? undefined,
        ),
      );
    }

    this.deps.logger?.info(
      "pipeline run finished pipelineId=%s runId=%s traceId=%s status=%s source=%s steps=%d errorCode=%s",
      pipeline.id,
      claimed.runId,
      claimed.traceId,
      finalRun.status,
      claimed.triggerSource,
      stepResults.length,
      finalRun.errorCode ?? "",
    );

    const ok = finalRun.status === "ok";
    return {
      ok,
      runId: claimed.runId,
      traceId: claimed.traceId,
      status: finalRun.status,
      ...(ok
        ? {}
        : {
            error: pipelineError ?? "pipeline failed",
            ...(finalRun.errorCode ? { errorCode: finalRun.errorCode } : {}),
          }),
    };
  }

  private abortOutcome(
    timeoutMs: number | undefined,
    startedAt: string,
  ): { status: "error" | "cancelled"; error: string; errorCode: string | null } {
    if (this.wasTimeout(timeoutMs, startedAt)) {
      return {
        status: "error",
        error: `Pipeline timed out after ${timeoutMs}ms`,
        errorCode: "PIPELINE_TIMEOUT",
      };
    }
    return { status: "cancelled", error: "Pipeline cancelled", errorCode: null };
  }

  private wasTimeout(timeoutMs: number | undefined, startedAt: string): boolean {
    if (typeof timeoutMs !== "number" || timeoutMs <= 0) return false;
    const elapsed = this.now().getTime() - Date.parse(startedAt);
    return Number.isFinite(elapsed) && elapsed >= timeoutMs - 25;
  }

  private async advanceNextRunAt(pipeline: Pipeline, completedAt: Date): Promise<void> {
    if (pipeline.trigger.kind !== "schedule") return;
    const fresh = await this.deps.store.get(pipeline.id);
    if (!fresh) return;
    const nextRunAt = nextRunAtForPipelineTrigger(
      fresh.trigger,
      completedAt.toISOString(),
      completedAt,
      fresh.enabled,
    );
    if (fresh.nextRunAt === nextRunAt) return;
    await this.deps.store.update({ ...fresh, nextRunAt });
  }

  private async touchStep(run: PipelineRun, stepId: string, status: "running", when: Date): Promise<PipelineRun> {
    const next: PipelineRun = {
      ...run,
      currentStepId: stepId,
      lastHeartbeatAt: when.toISOString(),
      leaseExpiresAt: new Date(when.getTime() + this.leaseTtlMs()).toISOString(),
      stepRuns: run.stepRuns.map((sr) =>
        sr.stepId === stepId
          ? { ...sr, status, startedAt: when.toISOString() }
          : sr,
      ),
    };
    await this.deps.store.updateRun(next);
    return next;
  }

  private async runStep(
    step: PipelineStep,
    context: Record<string, unknown>,
    templateContext: TemplateContext | undefined,
    pipeline: Pipeline,
    signal: AbortSignal,
  ): Promise<Omit<PipelineStepResult, "startedAt" | "completedAt">> {
    if (signal.aborted) {
      return { stepId: step.id, status: "cancelled", summary: "cancelled", error: "cancelled" };
    }

    if (step.condition) {
      // Evaluate step conditions against the pipeline step context directly:
      // `path` walks the accumulated context (outputKey map), e.g.
      // `{ path: "category" }` refers to the step that stored context.category.
      const conditionCtx: Record<string, unknown> = { ...context };
      if (!evaluateConditionNodeAgainstObject(step.condition, conditionCtx)) {
        this.deps.logger?.debug("pipeline %s step %s skipped (condition false)", pipeline.id, step.id);
        return { stepId: step.id, status: "skipped", summary: "condition not met" };
      }
    }

    if (step.action.type === "agent") {
      const prompt = templateContext
        ? resolveTemplates(step.action.prompt, templateContext)
        : step.action.prompt;
      try {
        const result = await this.deps.executor.runAgent(
          prompt,
          this.deps.executorSettings,
          signal,
          {
            ...(step.action.providerId ? { providerId: step.action.providerId } : {}),
            ...(step.action.model ? { model: step.action.model } : {}),
            ...(step.action.effort ? { effort: step.action.effort } : {}),
          },
        );
        if (signal.aborted) {
          return { stepId: step.id, status: "cancelled", summary: "cancelled", error: "cancelled" };
        }
        if (result.status === "error") {
          return { stepId: step.id, status: "error", summary: result.summary, error: result.error ?? result.summary };
        }
        return { stepId: step.id, status: "ok", summary: result.summary, output: result.summary };
      } catch (err) {
        if (signal.aborted) {
          return { stepId: step.id, status: "cancelled", summary: "cancelled", error: "cancelled" };
        }
        const msg = err instanceof Error ? err.message : String(err);
        return { stepId: step.id, status: "error", summary: msg, error: msg };
      }
    }

    const args = templateContext && step.action.args
      ? resolveTemplatesInRecord(step.action.args as Record<string, unknown>, templateContext)
      : step.action.args;
    const command: CallToolCommand = {
      kind: "call-tool",
      pluginId: step.action.pluginId,
      requestId: randomUUID(),
      toolName: step.action.toolName,
      args,
    };
    try {
      const result = await this.deps.callToolHandler.handle(command);
      if (signal.aborted) {
        return { stepId: step.id, status: "cancelled", summary: "cancelled", error: "cancelled" };
      }
      return { stepId: step.id, status: "ok", summary: String(result.result), output: result.result };
    } catch (err) {
      if (signal.aborted) {
        return { stepId: step.id, status: "cancelled", summary: "cancelled", error: "cancelled" };
      }
      const msg = err instanceof Error ? err.message : String(err);
      return { stepId: step.id, status: "error", summary: msg, error: msg };
    }
  }
}
