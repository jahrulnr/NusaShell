import { randomUUID } from "node:crypto";
import { ApplicationError } from "../../errors/application-error.js";
import type { JobStorePort } from "../../job/ports/job-store.port.js";
import type { JobScheduler } from "../../job/services/job-scheduler.js";
import type { Job, JobMode } from "../../job/job-model.js";
import type { ReasoningEffort } from "../ports/agent-provider.port.js";
import {
  parseSchedule,
  computeNextRun,
  describeSchedule,
  ScheduleParseError,
} from "../../job/schedule-parser.js";
import { clampInt, requireString, jobsNotConfigured } from "./gateway-utils.js";

export interface JobToolCallerContext {
  readonly providerId?: string;
  readonly model?: string;
  readonly effort?: ReasoningEffort;
}

/**
 * Foreground agent meta-tool `job` — full CRUD parity with the desktop Jobs
 * surface. Reuses the same schedule parser and store/scheduler ports as the
 * WS handlers; maps ApplicationError into structured envelopes so a bad action
 * never crashes the turn. Denied inside scheduled job turns (see
 * JobAgentToolGateway) to prevent recursion.
 */
export async function execJob(
  store: JobStorePort | undefined,
  scheduler: JobScheduler | undefined,
  args: Readonly<Record<string, unknown>>,
  caller?: JobToolCallerContext,
): Promise<unknown> {
  if (!store || !scheduler) return jobsNotConfigured();
  const action = requireString(args.action, "action");
  try {
    switch (action) {
      case "list": return await listJobs(store, scheduler);
      case "validate_schedule": return await validateSchedule(args);
      case "add": return await addJob(store, args, caller);
      case "update": return await updateJob(store, args, caller);
      case "set_enabled": return await setEnabled(store, args);
      case "run": return await runNow(scheduler, args);
      case "cancel": return await cancelJob(scheduler, args);
      case "remove": return await removeJob(store, args);
      case "output": return await jobOutput(store, args);
      default:
        throw new ApplicationError("AGENT_INVALID_INPUT", `Unsupported job action: ${action}`);
    }
  } catch (error) {
    return jobErrorEnvelope(error);
  }
}

async function listJobs(store: JobStorePort, scheduler?: JobScheduler): Promise<unknown> {
  const jobs = await store.list();
  const data = jobs.map((job) => compactJob(job, scheduler));
  return { ok: true, data: { jobs: data }, meta: { count: data.length } };
}

async function validateSchedule(args: Readonly<Record<string, unknown>>): Promise<unknown> {
  const schedule = requireString(args.schedule, "schedule");
  try {
    const parsed = parseSchedule(schedule);
    return { ok: true, data: { description: describeSchedule(parsed) }, meta: {} };
  } catch (error) {
    if (error instanceof ScheduleParseError) {
      return { ok: false, error: { code: "job_invalid_schedule", message: error.message }, meta: {} };
    }
    throw error;
  }
}

async function addJob(store: JobStorePort, args: Readonly<Record<string, unknown>>, caller?: JobToolCallerContext): Promise<unknown> {
  const name = requireString(args.name, "name");
  const scheduleInput = requireString(args.schedule, "schedule");
  const mode = parseJobMode(args, caller);
  const repeatTimes = parseRepeatTimes(args.repeat_times);
  let schedule;
  try {
    schedule = parseSchedule(scheduleInput);
  } catch (error) {
    if (error instanceof ScheduleParseError) {
      throw new ApplicationError("JOB_INVALID_SCHEDULE", error.message);
    }
    throw error;
  }
  const now = new Date();
  const nextRunAt = computeNextRun(schedule, null, now);
  const job: Job = {
    id: randomUUID(),
    name,
    schedule,
    mode,
    enabled: true,
    repeat: { times: repeatTimes, completed: 0 },
    nextRunAt,
    lastRunAt: null,
    lastStatus: null,
    lastError: null,
    createdAt: now.toISOString(),
  };
  const created = await store.create(job);
  return { ok: true, data: created, meta: {} };
}

async function updateJob(store: JobStorePort, args: Readonly<Record<string, unknown>>, caller?: JobToolCallerContext): Promise<unknown> {
  const id = requireString(args.id, "id");
  const existing = await store.get(id);
  if (!existing) throw new ApplicationError("JOB_NOT_FOUND", `Job not found: ${id}`);
  let schedule = existing.schedule;
  let nextRunAt = existing.nextRunAt;
  if (args.schedule !== undefined) {
    const scheduleInput = requireString(args.schedule, "schedule");
    try {
      schedule = parseSchedule(scheduleInput);
    } catch (error) {
      if (error instanceof ScheduleParseError) {
        throw new ApplicationError("JOB_INVALID_SCHEDULE", error.message);
      }
      throw error;
    }
    nextRunAt = existing.enabled ? computeNextRun(schedule, existing.lastRunAt, new Date()) : null;
  }
  const mode = args.mode !== undefined || args.prompt !== undefined || args.pluginId !== undefined
    ? parseJobMode(args, caller, existing.mode)
    : existing.mode;
  const repeatTimes = args.repeat_times !== undefined
    ? parseRepeatTimes(args.repeat_times)
    : existing.repeat.times;
  const enabled = args.enabled !== undefined ? parseBoolean(args.enabled, "enabled") : existing.enabled;
  const updated: Job = {
    ...existing,
    ...(args.name !== undefined ? { name: requireString(args.name, "name") } : {}),
    schedule,
    mode,
    enabled,
    repeat: { times: repeatTimes, completed: existing.repeat.completed },
    nextRunAt,
  };
  const result = await store.update(updated);
  return { ok: true, data: result, meta: {} };
}

async function setEnabled(store: JobStorePort, args: Readonly<Record<string, unknown>>): Promise<unknown> {
  const id = requireString(args.id, "id");
  const enabled = parseBoolean(args.enabled, "enabled");
  const job = await store.get(id);
  if (!job) throw new ApplicationError("JOB_NOT_FOUND", `Job not found: ${id}`);
  let nextRunAt = job.nextRunAt;
  if (enabled && job.schedule.kind !== "once" && (!nextRunAt || new Date(nextRunAt).getTime() <= Date.now())) {
    nextRunAt = computeNextRun(job.schedule, job.lastRunAt, new Date());
  }
  if (enabled && job.schedule.kind === "once" && !nextRunAt) {
    nextRunAt = job.schedule.runAt;
  }
  const updated: Job = { ...job, enabled, ...(nextRunAt !== job.nextRunAt ? { nextRunAt } : {}) };
  const result = await store.update(updated);
  return { ok: true, data: result, meta: {} };
}

async function runNow(scheduler: JobScheduler, args: Readonly<Record<string, unknown>>): Promise<unknown> {
  const id = requireString(args.id, "id");
  const result = await scheduler.runOneNow(id);
  if (!result.ok && result.error?.includes("not found")) {
    throw new ApplicationError("JOB_NOT_FOUND", result.error);
  }
  return { ok: true, data: result, meta: {} };
}

async function cancelJob(scheduler: JobScheduler, args: Readonly<Record<string, unknown>>): Promise<unknown> {
  const id = requireString(args.id, "id");
  const result = await scheduler.cancel(id);
  if (!result.ok && result.error?.includes("not running")) {
    throw new ApplicationError("JOB_NOT_RUNNING", result.error);
  }
  return { ok: true, data: result, meta: {} };
}

async function removeJob(store: JobStorePort, args: Readonly<Record<string, unknown>>): Promise<unknown> {
  const id = requireString(args.id, "id");
  const existing = await store.get(id);
  if (!existing) throw new ApplicationError("JOB_NOT_FOUND", `Job not found: ${id}`);
  await store.remove(id);
  return { ok: true, data: { id, removed: true }, meta: {} };
}

async function jobOutput(store: JobStorePort, args: Readonly<Record<string, unknown>>): Promise<unknown> {
  const id = requireString(args.id, "id");
  const limit = clampInt(args.limit, 20, 1, 100);
  const entries = await store.listOutputs(id, limit);
  return { ok: true, data: { entries }, meta: { count: entries.length } };
}

function parseJobMode(
  args: Readonly<Record<string, unknown>>,
  caller?: JobToolCallerContext,
  existing?: JobMode,
): JobMode {
  const type = args.mode !== undefined ? requireString(args.mode, "mode") : existing?.type;
  if (type === "agent") {
    const prompt = args.prompt !== undefined ? requireString(args.prompt, "prompt") : existing?.type === "agent" ? existing.prompt : undefined;
    if (!prompt) throw new ApplicationError("AGENT_INVALID_INPUT", "prompt is required for agent mode");
    if (prompt.length > 10000) {
      throw new ApplicationError("AGENT_INVALID_INPUT", "prompt must be 10000 characters or fewer");
    }
    const providerId = args.providerId !== undefined ? String(args.providerId) : undefined;
    const model = args.model !== undefined ? String(args.model) : undefined;
    const effort = args.effort !== undefined ? String(args.effort) as ReasoningEffort : undefined;
    // Precedence: explicit arg > existing mode field > caller turn's model.
    const existingAgent = existing?.type === "agent" ? existing : undefined;
    const resolvedProviderId = providerId ?? existingAgent?.providerId ?? caller?.providerId;
    const resolvedModel = model ?? existingAgent?.model ?? caller?.model;
    const resolvedEffort = effort ?? existingAgent?.effort ?? caller?.effort;
    return {
      type: "agent",
      prompt,
      ...(resolvedProviderId ? { providerId: resolvedProviderId } : {}),
      ...(resolvedModel ? { model: resolvedModel } : {}),
      ...(resolvedEffort ? { effort: resolvedEffort } : {}),
    };
  }
  if (type === "tool") {
    const pluginId = args.pluginId !== undefined ? requireString(args.pluginId, "pluginId") : existing?.type === "tool" ? existing.pluginId : undefined;
    const toolName = args.toolName !== undefined ? requireString(args.toolName, "toolName") : existing?.type === "tool" ? existing.toolName : undefined;
    if (!pluginId || !toolName) throw new ApplicationError("AGENT_INVALID_INPUT", "pluginId and toolName are required for tool mode");
    const rawArgs = args.args;
    let toolArgs: Readonly<Record<string, unknown>> = {};
    if (rawArgs !== undefined) {
      if (typeof rawArgs !== "object" || rawArgs === null || Array.isArray(rawArgs)) {
        throw new ApplicationError("AGENT_INVALID_INPUT", "args must be an object");
      }
      toolArgs = rawArgs as Readonly<Record<string, unknown>>;
    } else if (existing?.type === "tool") {
      toolArgs = existing.args;
    }
    return { type: "tool", pluginId, toolName, args: toolArgs };
  }
  throw new ApplicationError("AGENT_INVALID_INPUT", `mode must be "agent" or "tool"`);
}

function parseRepeatTimes(value: unknown): number | null {
  if (value === undefined || value === null) return null;
  const n = clampInt(value, NaN, 1, 100000);
  if (Number.isNaN(n)) throw new ApplicationError("AGENT_INVALID_INPUT", "repeat_times must be a positive integer");
  return n;
}

function parseBoolean(value: unknown, name: string): boolean {
  if (typeof value !== "boolean") throw new ApplicationError("AGENT_INVALID_INPUT", `${name} is required`);
  return value;
}

function compactJob(job: Job, scheduler?: JobScheduler): unknown {
  return {
    id: job.id,
    name: job.name,
    schedule: describeSchedule(job.schedule),
    enabled: job.enabled,
    nextRunAt: job.nextRunAt,
    lastStatus: job.lastStatus,
    running: scheduler?.isRunning(job.id) ?? false,
    ...(scheduler?.activeTraceId(job.id) ? { activeTraceId: scheduler.activeTraceId(job.id) } : {}),
  };
}

function jobErrorEnvelope(error: unknown): unknown {
  if (error instanceof ApplicationError) {
    const code = error.code === "JOB_NOT_FOUND"
      ? "job_not_found"
      : error.code === "JOB_INVALID_SCHEDULE"
        ? "job_invalid_schedule"
        : error.code === "JOB_NOT_RUNNING"
          ? "job_not_running"
          : "job_error";
    return { ok: false, error: { code, message: error.message }, meta: {} };
  }
  return {
    ok: false,
    error: { code: "job_error", message: error instanceof Error ? error.message : String(error) },
    meta: {},
  };
}
