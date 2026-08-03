/**
 * PipelineScheduler — executes a Pipeline's step DAG in topological order
 * (Phase E). Each step runs an agent turn or tool call, optionally
 * conditioned on accumulated context. Steps with `outputKey` store their
 * result in the context bag for downstream steps.
 *
 * Unlike JobScheduler (one action per job), PipelineScheduler holds a
 * per-run context across steps and evaluates per-step conditions against
 * that context.
 */

import { randomUUID } from "node:crypto";
import type { EventDispatcher } from "../../events/event-dispatcher.js";
import { createJobStartedEvent, createJobCompletedEvent, createJobFailedEvent } from "../../events/job-events.event.js";
import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import type { ReasoningEffort } from "../../agent/ports/agent-provider.port.js";
import type {
  Pipeline,
  PipelineStep,
  PipelineStepResult,
  PipelineRunResult,
} from "../pipeline-model.js";
import { topologicalSort, validatePipeline } from "../pipeline-model.js";
import type { PipelineStorePort } from "../ports/pipeline-store.port.js";
import type { TemplateContext } from "./job-template-resolver.js";
import { resolveTemplates, resolveTemplatesInRecord } from "./job-template-resolver.js";
import { evaluateConditionNode } from "./event-job-matcher.js";
import type { CallToolCommand } from "../../tool/commands/call-tool/call-tool.command.js";
import type { JobAgentExecutorSettings, JobExecutionResult } from "./job-agent-executor.js";

/** Reuses the same executor surface as JobScheduler. */
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
}

export class PipelineScheduler {
  constructor(private readonly deps: PipelineSchedulerDeps) {}

  /**
   * Run a pipeline by ID. Steps execute in topological order. Each step's
   * condition is evaluated against the accumulated context; if false, the
   * step is skipped. Steps with `outputKey` store their result in context.
   *
   * Template context from the triggering event (if any) is available to
   * step prompts/args as `{{event.*}}` and `{{payload.*}}`.
   */
  async runPipeline(
    pipelineId: string,
    templateContext?: TemplateContext,
  ): Promise<{ ok: boolean; error?: string }> {
    const pipeline = await this.deps.store.get(pipelineId);
    if (!pipeline) return { ok: false, error: "pipeline not found" };
    if (!pipeline.enabled) return { ok: false, error: "pipeline is disabled" };

    const validationError = validatePipeline(pipeline.steps);
    if (validationError) return { ok: false, error: validationError };

    const now = (this.deps.now ?? (() => new Date()))();
    const traceId = randomUUID();
    const startedAt = now.toISOString();
    const context: Record<string, unknown> = {};
    const stepResults: PipelineStepResult[] = [];
    const sorted = topologicalSort(pipeline.steps);

    await this.deps.eventDispatcher.publish(
      createJobStartedEvent(pipeline.id, pipeline.name, traceId, now, {
        type: "agent",
        prompt: `[pipeline: ${pipeline.name}]`,
      }),
    );

    let pipelineStatus: "ok" | "error" | "cancelled" = "ok";
    let pipelineError: string | null = null;

    try {
      for (const step of sorted) {
        const stepStart = (this.deps.now ?? (() => new Date()))();
        // Merge accumulated context into template context for this step.
        const stepTemplateCtx = templateContext
          ? { ...templateContext, context: { ...context } }
          : { event: { type: "", pluginId: "", payload: {} }, context: { ...context } };
        const stepResult = await this.runStep(step, context, stepTemplateCtx, pipeline);
        stepResults.push({
          ...stepResult,
          startedAt: stepStart.toISOString(),
          completedAt: (this.deps.now ?? (() => new Date()))().toISOString(),
        });

        if (stepResult.status === "error") {
          pipelineStatus = "error";
          pipelineError = `Step "${step.id}" failed: ${stepResult.error ?? stepResult.summary}`;
          break;
        }
        if (stepResult.status === "ok" && step.outputKey && stepResult.output !== undefined) {
          context[step.outputKey] = stepResult.output;
        }
      }
    } catch (err) {
      pipelineStatus = "error";
      pipelineError = err instanceof Error ? err.message : String(err);
    }

    const completedAt = (this.deps.now ?? (() => new Date()))();
    await this.deps.store.markRun(pipeline.id, pipelineStatus, pipelineError, completedAt);

    const runResult: PipelineRunResult = {
      pipelineId: pipeline.id,
      status: pipelineStatus,
      context,
      stepResults,
      startedAt,
      completedAt: completedAt.toISOString(),
      ...(pipelineError ? { error: pipelineError } : {}),
    };

    if (pipelineStatus === "ok") {
      await this.deps.eventDispatcher.publish(
        createJobCompletedEvent(pipeline.id, pipeline.name, `Pipeline completed (${stepResults.length} steps)`, completedAt, traceId),
      );
    } else {
      await this.deps.eventDispatcher.publish(
        createJobFailedEvent(pipeline.id, pipeline.name, pipelineError ?? "unknown", completedAt, traceId),
      );
    }

    this.deps.logger?.info(
      "pipeline %s run finished: status=%s steps=%d",
      pipeline.id,
      pipelineStatus,
      stepResults.length,
    );

    void runResult; // available for future output persistence
    return pipelineStatus === "ok"
      ? { ok: true }
      : { ok: false, error: pipelineError ?? "pipeline failed" };
  }

  private async runStep(
    step: PipelineStep,
    context: Record<string, unknown>,
    templateContext: TemplateContext | undefined,
    pipeline: Pipeline,
  ): Promise<Omit<PipelineStepResult, "startedAt" | "completedAt">> {
    // Evaluate condition against accumulated context + event context.
    if (step.condition) {
      const conditionCtx = { ...context, ...(templateContext?.event ? { event: templateContext.event } : {}) };
      if (!evaluateConditionNode(step.condition, { type: "pipeline.step", pluginId: "", payload: conditionCtx } as never)) {
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
          undefined,
          {
            ...(step.action.providerId ? { providerId: step.action.providerId } : {}),
            ...(step.action.model ? { model: step.action.model } : {}),
            ...(step.action.effort ? { effort: step.action.effort } : {}),
          },
        );
        if (result.status === "error") {
          return { stepId: step.id, status: "error", summary: result.summary, error: result.error ?? result.summary };
        }
        return { stepId: step.id, status: "ok", summary: result.summary, output: result.summary };
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err);
        return { stepId: step.id, status: "error", summary: msg, error: msg };
      }
    } else {
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
        return { stepId: step.id, status: "ok", summary: String(result.result), output: result.result };
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err);
        return { stepId: step.id, status: "error", summary: msg, error: msg };
      }
    }
  }
}
