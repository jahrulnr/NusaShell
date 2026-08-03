import type { EventDispatcher } from "../../events/event-dispatcher.js";
import type { AutomationEvent } from "../../events/automation-event.js";
import type { JobStorePort } from "../ports/job-store.port.js";
import type { JobScheduler } from "./job-scheduler.js";
import type { Job, Condition, ConditionNode, JobTrigger } from "../job-model.js";
import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import { templateContextFromEvent, type TemplateContext } from "./job-template-resolver.js";

/**
 * EventJobMatcher subscribes to `AutomationEvent`s on the existing
 * `EventDispatcher`, matches them against enabled event-triggered Jobs, and
 * fires matching jobs via `JobScheduler.runOneNow`.
 *
 * Throttle semantics (see 02-job-triggers.md §7):
 * - `throttleMs`: coalesce — within the throttle window, the latest matching
 *   event's payload is retained and scheduled to fire at window end. An even
 *   newer event replaces the retained one.
 * - `maxFiresPerHour`: hard cap — once hit, events are dropped until the
 *   rolling window frees up.
 *
 * Order of application: pattern match → conditions → maxFiresPerHour →
 * throttleMs coalesce → dispatch.
 */
/** Phase D: max chain depth to prevent infinite loops (second line of defense). */
export const MAX_CHAIN_DEPTH = 8;

export class EventJobMatcher {
  private readonly fireTimestamps = new Map<string, number[]>(); // jobId → rolling hour
  private readonly lastFireAt = new Map<string, number>(); // jobId → ms
  private readonly pendingCoalesce = new Map<string, { event: AutomationEvent; fireAt: number }>();
  private readonly coalesceTimers = new Map<string, ReturnType<typeof setTimeout>>();
  private started = false;

  constructor(
    private readonly deps: {
      readonly store: JobStorePort;
      readonly scheduler: JobScheduler;
      readonly eventDispatcher: EventDispatcher;
      readonly logger?: LoggerPort;
      readonly now?: () => Date;
      /** Phase E: optional pipeline store + scheduler for event-triggered pipelines. */
      readonly pipelineStore?: import("../ports/pipeline-store.port.js").PipelineStorePort;
      readonly pipelineScheduler?: import("./pipeline-scheduler.js").PipelineScheduler;
    },
  ) {}

  start(): void {
    if (this.started) return;
    this.started = true;
    this.deps.eventDispatcher.on("automation.event", {
      handle: (event) => this.handleEvent(event as AutomationEvent),
    });
  }

  stop(): void {
    if (!this.started) return;
    this.started = false;
    for (const timer of this.coalesceTimers.values()) clearTimeout(timer);
    this.coalesceTimers.clear();
    this.pendingCoalesce.clear();
  }

  private async handleEvent(event: AutomationEvent): Promise<void> {
    try {
      const jobs = await this.deps.store.list();
      const now = (this.deps.now ?? (() => new Date()))().getTime();
      const chainDepth = event.chainDepth ?? 0;
      for (const job of jobs) {
        if (!job.enabled) continue;
        if (job.trigger.kind !== "event") continue;
        if (!this.matches(job.trigger, event)) continue;
        // Cycle guard (Phase D): block self-trigger and deep chains.
        if (event.originJobId === job.id) {
          this.deps.logger?.warn(
            "event-job %s blocked: self-trigger cycle (event origin = this job)",
            job.id,
          );
          continue;
        }
        if (chainDepth >= MAX_CHAIN_DEPTH) {
          this.deps.logger?.warn(
            "event-job %s blocked: chain depth %d exceeds max %d",
            job.id,
            chainDepth,
            MAX_CHAIN_DEPTH,
          );
          continue;
        }
        await this.considerFire(job, event, now);
      }
      // Phase E: match event-triggered pipelines.
      if (this.deps.pipelineStore && this.deps.pipelineScheduler) {
        await this.matchPipelines(event);
      }
    } catch (error) {
      this.deps.logger?.error(
        "event-job-matcher error: %s",
        error instanceof Error ? error.message : String(error),
      );
    }
  }

  /** Test whether a trigger matches an event (pattern + pluginId + conditions). */
  matches(trigger: Extract<JobTrigger, { kind: "event" }>, event: AutomationEvent): boolean {
    if (trigger.pluginId !== undefined && event.pluginId !== trigger.pluginId) return false;
    if (!matchGlob(trigger.pattern, event.eventType)) return false;
    if (trigger.conditions) {
      for (const node of trigger.conditions) {
        if (!evaluateConditionNode(node, event)) return false;
      }
    }
    return true;
  }

  /** Phase E: match event-triggered pipelines and run them. */
  private async matchPipelines(event: AutomationEvent): Promise<void> {
    if (!this.deps.pipelineStore || !this.deps.pipelineScheduler) return;
    const pipelines = await this.deps.pipelineStore.list();
    const ctx = templateContextFromEvent(event);
    for (const pipeline of pipelines) {
      if (!pipeline.enabled) continue;
      if (pipeline.trigger.kind !== "event") continue;
      if (!this.matches(pipeline.trigger, event)) continue;
      this.deps.logger?.info(
        "pipeline %s firing for event %s",
        pipeline.id,
        event.eventType,
      );
      const result = await this.deps.pipelineScheduler.runPipeline(pipeline.id, ctx);
      if (!result.ok) {
        this.deps.logger?.debug("pipeline %s run failed: %s", pipeline.id, result.error ?? "unknown");
      }
    }
  }

  private async considerFire(job: Job, event: AutomationEvent, now: number): Promise<void> {
    const trigger = job.trigger as Extract<JobTrigger, { kind: "event" }>;

    // maxFiresPerHour check (drop, not coalesce)
    if (trigger.maxFiresPerHour !== undefined) {
      if (!this.withinHourlyCap(job.id, trigger.maxFiresPerHour, now)) {
        this.deps.logger?.debug(
          "event-job %s dropped: maxFiresPerHour (%d) reached",
          job.id,
          trigger.maxFiresPerHour,
        );
        return;
      }
    }

    // throttleMs coalesce
    if (trigger.throttleMs !== undefined && trigger.throttleMs > 0) {
      const lastFire = this.lastFireAt.get(job.id) ?? 0;
      const elapsed = now - lastFire;
      if (elapsed < trigger.throttleMs) {
        const fireAt = lastFire + trigger.throttleMs;
        this.scheduleCoalesced(job, event, fireAt);
        return;
      }
    }

    await this.fire(job, event, now);
  }

  private async fire(job: Job, event: AutomationEvent, now: number): Promise<void> {
    this.lastFireAt.set(job.id, now);
    this.recordFire(job.id, now);
    this.deps.logger?.info(
      "event-job %s firing for event %s (plugin=%s)",
      job.id,
      event.eventType,
      event.pluginId ?? "any",
    );
    const ctx: TemplateContext = templateContextFromEvent(event);
    const chainOrigin = event.originJobId !== undefined
      ? { originJobId: event.originJobId, chainDepth: event.chainDepth ?? 0 }
      : undefined;
    const result = await this.deps.scheduler.runOneNow(job.id, ctx, chainOrigin);
    if (!result.ok) {
      this.deps.logger?.debug("event-job %s fire skipped: %s", job.id, result.error ?? "unknown");
    }
  }

  private scheduleCoalesced(job: Job, event: AutomationEvent, fireAt: number): void {
    // Replace any pending coalesced event for this job (latest wins).
    this.pendingCoalesce.set(job.id, { event, fireAt });
    const existing = this.coalesceTimers.get(job.id);
    if (existing) clearTimeout(existing);
    const delay = Math.max(0, fireAt - (this.deps.now ?? (() => new Date()))().getTime());
    const timer = setTimeout(() => {
      void this.flushCoalesced(job.id);
    }, delay);
    this.coalesceTimers.set(job.id, timer);
  }

  private async flushCoalesced(jobId: string): Promise<void> {
    this.coalesceTimers.delete(jobId);
    const pending = this.pendingCoalesce.get(jobId);
    if (!pending) return;
    this.pendingCoalesce.delete(jobId);
    const now = (this.deps.now ?? (() => new Date()))().getTime();
    const job = await this.deps.store.get(jobId);
    if (!job || !job.enabled) return;
    await this.fire(job, pending.event, now);
  }

  private withinHourlyCap(jobId: string, max: number, now: number): boolean {
    const windowMs = 60 * 60 * 1000;
    const stamps = this.fireTimestamps.get(jobId) ?? [];
    const recent = stamps.filter((t) => now - t < windowMs);
    this.fireTimestamps.set(jobId, recent);
    return recent.length < max;
  }

  private recordFire(jobId: string, now: number): void {
    const stamps = this.fireTimestamps.get(jobId) ?? [];
    stamps.push(now);
    this.fireTimestamps.set(jobId, stamps);
  }
}

/**
 * Glob matcher for event type patterns. Supports `*` (single segment) and
 * `**` (multi-segment). Uses a compiled regex for matching.
 *
 * Examples: "mail.new" (exact), "mail.*" (all mail), "**.updated" (any .updated).
 */
export function matchGlob(pattern: string, eventType: string): boolean {
  if (pattern === eventType) return true;
  if (!pattern.includes("*")) return false;
  const regex = globToRegex(pattern);
  return regex.test(eventType);
}

function globToRegex(pattern: string): RegExp {
  let out = "^";
  let i = 0;
  while (i < pattern.length) {
    const ch = pattern[i]!;
    if (ch === "*") {
      if (pattern[i + 1] === "*") {
        out += ".*";
        i += 2;
        if (pattern[i] === ".") i++; // consume separator after **
      } else {
        out += "[^.]*";
        i += 1;
      }
    } else if (".+?^${}()|[]\\".includes(ch)) {
      out += "\\" + ch;
      i += 1;
    } else {
      out += ch;
      i += 1;
    }
  }
  out += "$";
  return new RegExp(out);
}

/**
 * Evaluate a single condition against an event. A missing path means
 * no-match (distinct from template resolution where missing = literal).
 * Phase D adds `ne` (not-equal).
 */
export function evaluateCondition(cond: Condition, event: AutomationEvent): boolean {
  const resolved = resolveDotPath(event, cond.path);
  if (resolved === undefined) return false;
  const str = String(resolved);
  switch (cond.op) {
    case "eq":
      return str === cond.value;
    case "ne":
      return str !== cond.value;
    case "contains":
      return str.includes(cond.value);
    case "regex":
      try {
        return new RegExp(cond.value).test(str);
      } catch {
        return false;
      }
  }
}

/**
 * Evaluate a condition node (leaf Condition or nested group with OR/NOT).
 * Phase D adds support for `{ op: "or", any: [...] }` and `{ op: "not", of: ... }`.
 */
export function evaluateConditionNode(node: ConditionNode, event: AutomationEvent): boolean {
  if ("path" in node) return evaluateCondition(node, event);
  if (node.op === "or") return node.any.some((child) => evaluateConditionNode(child, event));
  if (node.op === "not") return !evaluateConditionNode(node.of, event);
  return false;
}

/** Resolve a dot-path like "payload.subject" against an object. */
export function resolveDotPath(obj: unknown, path: string): unknown {
  const parts = path.split(".");
  let current: unknown = obj;
  for (const part of parts) {
    if (current === null || typeof current !== "object") return undefined;
    current = (current as Record<string, unknown>)[part];
  }
  return current;
}
