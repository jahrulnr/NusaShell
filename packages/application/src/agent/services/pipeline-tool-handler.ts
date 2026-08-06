import { randomUUID } from "node:crypto";
import { ApplicationError } from "../../errors/application-error.js";
import type { PipelineStorePort } from "../../job/ports/pipeline-store.port.js";
import type { PipelineScheduler } from "../../job/services/pipeline-scheduler.js";
import type {
  Pipeline,
  PipelineStep,
  PipelineStepAction,
} from "../../job/pipeline-model.js";
import {
  nextRunAtForPipelineTrigger,
  validatePipeline,
  validatePipelineTrigger,
} from "../../job/pipeline-model.js";
import type { JobTrigger, ConditionNode, Condition } from "../../job/job-model.js";
import { scheduleOf } from "../../job/job-model.js";
import {
  describeSchedule,
  parseSchedule,
  ScheduleParseError,
} from "../../job/schedule-parser.js";
import { clampInt, requireString, jobsNotConfigured } from "./gateway-utils.js";

/**
 * Foreground agent meta-tool `pipeline` — full CRUD parity with the desktop
 * Pipelines surface. Reuses the same store/scheduler ports as the WS handlers.
 * Denied inside scheduled job/pipeline turns (no recursion).
 */
export async function execPipeline(
  store: PipelineStorePort | undefined,
  scheduler: PipelineScheduler | undefined,
  args: Readonly<Record<string, unknown>>,
): Promise<unknown> {
  if (!store || !scheduler) return jobsNotConfigured();
  const action = requireString(args.action, "action");
  try {
    switch (action) {
      case "list": return await listPipelines(store);
      case "add": return await addPipeline(store, args);
      case "update": return await updatePipeline(store, args);
      case "remove": return await removePipeline(store, args);
      case "run": return await runPipeline(scheduler, args);
      case "cancel": return await cancelPipeline(scheduler, args);
      case "runs": return await listRuns(store, args);
      case "run_get": return await getRun(store, args);
      default:
        throw new ApplicationError("AGENT_INVALID_INPUT", `Unsupported pipeline action: ${action}`);
    }
  } catch (error) {
    return pipelineErrorEnvelope(error);
  }
}

async function listPipelines(store: PipelineStorePort): Promise<unknown> {
  const pipelines = await store.list();
  return {
    ok: true,
    data: pipelines.map(compactPipeline),
    meta: { count: pipelines.length },
  };
}

async function addPipeline(store: PipelineStorePort, args: Readonly<Record<string, unknown>>): Promise<unknown> {
  const name = requireString(args.name, "name");
  const trigger = parseTriggerFromArgs(args);
  const triggerError = validatePipelineTrigger(trigger);
  if (triggerError) {
    throw new ApplicationError(
      trigger.kind === "schedule" ? "JOB_INVALID_SCHEDULE" : "PIPELINE_INVALID",
      triggerError,
    );
  }
  const steps = parseSteps(args.steps);
  const validationError = validatePipeline(steps);
  if (validationError) {
    throw new ApplicationError("PIPELINE_INVALID", validationError);
  }
  const now = new Date();
  const pipeline: Pipeline = {
    id: randomUUID(),
    name,
    ...(typeof args.description === "string" ? { description: args.description } : {}),
    enabled: true,
    trigger,
    steps,
    createdAt: now.toISOString(),
    nextRunAt: nextRunAtForPipelineTrigger(trigger, null, now, true),
    lastRunAt: null,
    lastStatus: null,
    lastError: null,
  };
  const created = await store.create(pipeline);
  return { ok: true, data: compactPipeline(created), meta: {} };
}

async function updatePipeline(store: PipelineStorePort, args: Readonly<Record<string, unknown>>): Promise<unknown> {
  const id = requireString(args.id, "id");
  const existing = await store.get(id);
  if (!existing) throw new ApplicationError("PIPELINE_NOT_FOUND", `Pipeline not found: ${id}`);
  const steps = args.steps !== undefined ? parseSteps(args.steps) : existing.steps;
  if (args.steps !== undefined) {
    const validationError = validatePipeline(steps);
    if (validationError) {
      throw new ApplicationError("PIPELINE_INVALID", validationError);
    }
  }
  const trigger = args.trigger !== undefined || args.schedule !== undefined
    ? parseTriggerFromArgs(args)
    : existing.trigger;
  if (args.trigger !== undefined || args.schedule !== undefined) {
    const triggerError = validatePipelineTrigger(trigger);
    if (triggerError) {
      throw new ApplicationError(
        trigger.kind === "schedule" ? "JOB_INVALID_SCHEDULE" : "PIPELINE_INVALID",
        triggerError,
      );
    }
  }
  const enabled = args.enabled !== undefined ? parseBoolean(args.enabled, "enabled") : existing.enabled;
  const now = new Date();
  const recomputeNext =
    args.trigger !== undefined || args.schedule !== undefined || args.enabled !== undefined;
  const updated: Pipeline = {
    ...existing,
    ...(args.name !== undefined ? { name: requireString(args.name, "name") } : {}),
    ...(args.description !== undefined && typeof args.description === "string" ? { description: args.description } : {}),
    trigger,
    steps,
    enabled,
    ...(recomputeNext
      ? { nextRunAt: nextRunAtForPipelineTrigger(trigger, existing.lastRunAt, now, enabled) }
      : {}),
  };
  const result = await store.update(updated);
  return { ok: true, data: compactPipeline(result), meta: {} };
}

async function removePipeline(store: PipelineStorePort, args: Readonly<Record<string, unknown>>): Promise<unknown> {
  const id = requireString(args.id, "id");
  const existing = await store.get(id);
  if (!existing) throw new ApplicationError("PIPELINE_NOT_FOUND", `Pipeline not found: ${id}`);
  await store.remove(id);
  return { ok: true, data: { id, removed: true }, meta: {} };
}

async function runPipeline(scheduler: PipelineScheduler, args: Readonly<Record<string, unknown>>): Promise<unknown> {
  const id = requireString(args.id, "id");
  // Fire-and-track: launch returns immediately with runId/traceId; progress
  // is reported via pipeline.* events and run_get/runs queries.
  const result = await scheduler.launch(id, undefined, { source: "manual" });
  if (!result.ok && result.errorCode === "PIPELINE_NOT_FOUND") {
    throw new ApplicationError("PIPELINE_NOT_FOUND", result.error ?? "pipeline not found");
  }
  return { ok: true, data: result, meta: {} };
}

async function cancelPipeline(scheduler: PipelineScheduler, args: Readonly<Record<string, unknown>>): Promise<unknown> {
  const id = requireString(args.id, "id");
  const result = await scheduler.cancel(id);
  return { ok: result.ok, data: result, meta: {} };
}

async function listRuns(store: PipelineStorePort, args: Readonly<Record<string, unknown>>): Promise<unknown> {
  const id = requireString(args.id, "id");
  const existing = await store.get(id);
  if (!existing) throw new ApplicationError("PIPELINE_NOT_FOUND", `Pipeline not found: ${id}`);
  const limit = args.limit !== undefined ? clampInt(args.limit, 20, 1, 100) : 20;
  const includeBody = args.include_body === true || args.includeBody === true;
  const runs = await store.listRuns(id, limit);
  const data = includeBody
    ? runs
    : runs.map((run) => ({
        ...run,
        stepRuns: run.stepRuns.map((sr) => {
          const { outputPreview: _preview, ...rest } = sr;
          return rest;
        }),
      }));
  return { ok: true, data, meta: { count: data.length } };
}

async function getRun(store: PipelineStorePort, args: Readonly<Record<string, unknown>>): Promise<unknown> {
  const runId = requireString(args.run_id ?? args.runId, "run_id");
  const run = await store.getRun(runId);
  if (!run) throw new ApplicationError("PIPELINE_RUN_NOT_FOUND", `Pipeline run not found: ${runId}`);
  const includeBody = args.include_body === true || args.includeBody === true;
  if (includeBody) return { ok: true, data: run, meta: {} };
  const { stepRuns, ...rest } = run;
  return {
    ok: true,
    data: {
      ...rest,
      stepRuns: stepRuns.map((sr) => {
        const { outputPreview: _preview, ...srest } = sr;
        return srest;
      }),
    },
    meta: {},
  };
}

function parseTriggerFromArgs(args: Readonly<Record<string, unknown>>): JobTrigger {
  if (args.schedule !== undefined) {
    const input = requireString(args.schedule, "schedule");
    try {
      const schedule = parseSchedule(input);
      return { kind: "schedule", schedule };
    } catch (error) {
      if (error instanceof ScheduleParseError) {
        throw new ApplicationError("JOB_INVALID_SCHEDULE", error.message);
      }
      throw error;
    }
  }
  if (args.trigger !== undefined) {
    return parseTriggerObject(args.trigger);
  }
  throw new ApplicationError("AGENT_INVALID_INPUT", "`trigger` or `schedule` is required");
}

function parseTriggerObject(raw: unknown): JobTrigger {
  if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "trigger must be an object");
  }
  const obj = raw as Record<string, unknown>;
  const kind = obj.kind;
  if (kind === "schedule") {
    if (typeof obj.schedule === "string") {
      try {
        return { kind: "schedule", schedule: parseSchedule(obj.schedule) };
      } catch (error) {
        if (error instanceof ScheduleParseError) {
          throw new ApplicationError("JOB_INVALID_SCHEDULE", error.message);
        }
        throw error;
      }
    }
    if (typeof obj.schedule !== "object" || obj.schedule === null || Array.isArray(obj.schedule)) {
      throw new ApplicationError("AGENT_INVALID_INPUT", "trigger.schedule must be an object or string");
    }
    const schedule = obj.schedule as Record<string, unknown>;
    if (schedule.kind === "once") {
      return { kind: "schedule", schedule: { kind: "once", runAt: requireString(schedule.runAt, "schedule.runAt") } };
    }
    if (schedule.kind === "interval") {
      const minutes = clampInt(schedule.minutes, NaN, 1, 525600);
      return { kind: "schedule", schedule: { kind: "interval", minutes } };
    }
    if (schedule.kind === "cron") {
      return { kind: "schedule", schedule: { kind: "cron", expr: requireString(schedule.expr, "schedule.expr") } };
    }
    throw new ApplicationError("AGENT_INVALID_INPUT", "trigger.schedule.kind must be once, interval, or cron");
  }
  if (kind === "event") {
    const pattern = requireString(obj.pattern, "trigger.pattern");
    const trigger: {
      kind: "event";
      pattern: string;
      pluginId?: string;
      conditions?: Condition[];
      throttleMs?: number;
      maxFiresPerHour?: number;
    } = {
      kind: "event",
      pattern,
    };
    if (obj.pluginId !== undefined && obj.pluginId !== null) {
      trigger.pluginId = requireString(obj.pluginId, "trigger.pluginId");
    }
    if (obj.conditions !== undefined && obj.conditions !== null) {
      if (!Array.isArray(obj.conditions)) {
        throw new ApplicationError("AGENT_INVALID_INPUT", "trigger.conditions must be an array");
      }
      trigger.conditions = obj.conditions.map(parseCondition);
    }
    if (obj.throttleMs !== undefined && obj.throttleMs !== null) {
      trigger.throttleMs = clampInt(obj.throttleMs, NaN, 0, 86400000);
    }
    if (obj.maxFiresPerHour !== undefined && obj.maxFiresPerHour !== null) {
      trigger.maxFiresPerHour = clampInt(obj.maxFiresPerHour, NaN, 1, 100000);
    }
    return trigger;
  }
  throw new ApplicationError("AGENT_INVALID_INPUT", `trigger.kind must be "event" or "schedule"`);
}

function parseCondition(raw: unknown): Condition {
  if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "condition must be an object");
  }
  const obj = raw as Record<string, unknown>;
  const path = requireString(obj.path, "path");
  const op = obj.op;
  if (op !== "eq" && op !== "ne" && op !== "contains" && op !== "regex") {
    throw new ApplicationError("AGENT_INVALID_INPUT", "condition.op must be eq, ne, contains, or regex");
  }
  const value = requireString(obj.value, "value");
  if (op === "regex") {
    try {
      new RegExp(value);
    } catch {
      throw new ApplicationError("AGENT_INVALID_INPUT", `condition regex is invalid: ${value}`);
    }
  }
  return { path, op, value };
}

function parseSteps(raw: unknown): PipelineStep[] {
  if (!Array.isArray(raw)) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "steps must be an array");
  }
  if (raw.length === 0) {
    throw new ApplicationError("PIPELINE_INVALID", "pipeline must have at least one step");
  }
  return raw.map((item, i) => parseStep(item, i));
}

function parseStep(raw: unknown, index: number): PipelineStep {
  if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
    throw new ApplicationError("AGENT_INVALID_INPUT", `step[${index}] must be an object`);
  }
  const obj = raw as Record<string, unknown>;
  const id = requireString(obj.id, `step[${index}].id`);
  const name = requireString(obj.name, `step[${index}].name`);
  const action = parseStepAction(obj.action, index);
  const dependsOn = obj.dependsOn !== undefined && obj.dependsOn !== null
    ? Array.isArray(obj.dependsOn)
      ? obj.dependsOn.map((d, j) => requireString(d, `step[${index}].dependsOn[${j}]`))
      : (() => { throw new ApplicationError("AGENT_INVALID_INPUT", `step[${index}].dependsOn must be an array`); })()
    : undefined;
  const outputKey = obj.outputKey !== undefined && obj.outputKey !== null
    ? requireString(obj.outputKey, `step[${index}].outputKey`)
    : undefined;
  const condition = obj.condition !== undefined && obj.condition !== null
    ? parseConditionNode(obj.condition, index)
    : undefined;
  return {
    id,
    name,
    action,
    ...(dependsOn ? { dependsOn } : {}),
    ...(outputKey ? { outputKey } : {}),
    ...(condition ? { condition } : {}),
  };
}

function parseStepAction(raw: unknown, index: number): PipelineStepAction {
  if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
    throw new ApplicationError("AGENT_INVALID_INPUT", `step[${index}].action must be an object`);
  }
  const obj = raw as Record<string, unknown>;
  const type = obj.type;
  if (type === "agent") {
    const prompt = requireString(obj.prompt, `step[${index}].action.prompt`);
    return {
      type: "agent",
      prompt,
      ...(obj.providerId !== undefined && obj.providerId !== null ? { providerId: requireString(obj.providerId, `step[${index}].action.providerId`) } : {}),
      ...(obj.model !== undefined && obj.model !== null ? { model: requireString(obj.model, `step[${index}].action.model`) } : {}),
      ...(obj.effort !== undefined && obj.effort !== null ? { effort: requireString(obj.effort, `step[${index}].action.effort`) as "low" | "medium" | "high" } : {}),
    } as PipelineStepAction;
  }
  if (type === "tool") {
    const pluginId = requireString(obj.pluginId, `step[${index}].action.pluginId`);
    const toolName = requireString(obj.toolName, `step[${index}].action.toolName`);
    if (obj.args === undefined || typeof obj.args !== "object" || obj.args === null || Array.isArray(obj.args)) {
      throw new ApplicationError("AGENT_INVALID_INPUT", `step[${index}].action.args must be an object`);
    }
    return { type: "tool", pluginId, toolName, args: obj.args as Readonly<Record<string, unknown>> };
  }
  throw new ApplicationError("AGENT_INVALID_INPUT", `step[${index}].action.type must be "agent" or "tool"`);
}

function parseConditionNode(raw: unknown, index: number): ConditionNode {
  if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
    throw new ApplicationError("AGENT_INVALID_INPUT", `step[${index}].condition must be an object`);
  }
  const obj = raw as Record<string, unknown>;
  if (obj.op === "or") {
    if (!Array.isArray(obj.any)) {
      throw new ApplicationError("AGENT_INVALID_INPUT", `step[${index}].condition.any must be an array`);
    }
    return { op: "or", any: obj.any.map((c) => parseConditionNode(c, index)) };
  }
  if (obj.op === "not") {
    if (obj.of === undefined) {
      throw new ApplicationError("AGENT_INVALID_INPUT", `step[${index}].condition.of is required for op=not`);
    }
    return { op: "not", of: parseConditionNode(obj.of, index) };
  }
  return parseCondition(raw);
}

function parseBoolean(value: unknown, name: string): boolean {
  if (typeof value !== "boolean") throw new ApplicationError("AGENT_INVALID_INPUT", `${name} is required`);
  return value;
}

function compactPipeline(p: Pipeline): unknown {
  const schedule = scheduleOf(p.trigger);
  const triggerDesc = schedule
    ? describeSchedule(schedule)
    : p.trigger.kind === "event" ? `event ${p.trigger.pattern}` : "—";
  return {
    id: p.id,
    name: p.name,
    ...(p.description ? { description: p.description } : {}),
    trigger: triggerDesc,
    enabled: p.enabled,
    steps: p.steps.length,
    nextRunAt: p.nextRunAt,
    lastRunAt: p.lastRunAt,
    lastStatus: p.lastStatus,
    ...(p.lastError ? { lastError: p.lastError } : {}),
  };
}

function pipelineErrorEnvelope(error: unknown): unknown {
  if (error instanceof ApplicationError) {
    const code = error.code === "PIPELINE_NOT_FOUND"
      ? "pipeline_not_found"
      : error.code === "PIPELINE_INVALID"
        ? "pipeline_invalid"
        : error.code === "PIPELINE_SCHEDULE_UNSUPPORTED"
          ? "pipeline_schedule_unsupported"
          : error.code === "PIPELINE_RUN_NOT_FOUND"
            ? "pipeline_run_not_found"
          : error.code === "JOB_INVALID_SCHEDULE"
            ? "job_invalid_schedule"
            : "internal_error";
    return { ok: false, error: { code, message: error.message } };
  }
  return { ok: false, error: { code: "internal_error", message: String(error) } };
}
