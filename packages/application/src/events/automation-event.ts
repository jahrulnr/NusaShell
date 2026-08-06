import { randomUUID } from "node:crypto";
import type { DomainEvent } from "@nusashell/domain";

/**
 * AutomationEvent — the internal envelope for plugin-emitted (or synthetic)
 * events that event-triggered Jobs match against.
 *
 * Sources v1: MCP `resources/updated`, `notifications/nusashell/automation`,
 * timer (existing schedule jobs), manual `job.run`.
 * Sources deferred: host webhook server, host chokidar, OS daemon.
 *
 * See tmp/plan/watch-to-agent/01-architecture.md and 04-mcp-automation-contract.md.
 */
export interface AutomationEvent extends DomainEvent {
  readonly type: "automation.event";
  readonly eventType: string; // e.g. "mail.new", "resource.updated"
  readonly pluginId?: string;
  readonly payload: Readonly<Record<string, unknown>>;
  readonly eventId: string;
  /** Phase D: set when this event was emitted by a job's onComplete hook. */
  readonly originJobId?: string;
  /** Phase E: set when this event was emitted by a pipeline completion/failure. */
  readonly originPipelineId?: string;
  /** Phase D: chain depth from the originating job/pipeline (0 = plugin-emitted, 1+ = chain). */
  readonly chainDepth?: number;
}

/**
 * Create an AutomationEvent. `occurredAt` defaults to now; `aggregateId` is
 * derived from the event type + id so the EventDispatcher can route it.
 */
export function createAutomationEvent(
  eventType: string,
  pluginId: string | undefined,
  payload: Readonly<Record<string, unknown>>,
  occurredAt: Date = new Date(),
  eventId: string = randomUUID(),
  origin?:
    | { readonly jobId: string; readonly chainDepth: number }
    | { readonly pipelineId: string; readonly chainDepth: number },
): AutomationEvent {
  const originFields = origin
    ? "jobId" in origin
      ? { originJobId: origin.jobId, chainDepth: origin.chainDepth }
      : { originPipelineId: origin.pipelineId, chainDepth: origin.chainDepth }
    : {};
  return {
    type: "automation.event",
    aggregateId: `automation:${eventId}`,
    occurredAt,
    eventType,
    ...(pluginId !== undefined ? { pluginId } : {}),
    payload,
    eventId,
    ...originFields,
  };
}
