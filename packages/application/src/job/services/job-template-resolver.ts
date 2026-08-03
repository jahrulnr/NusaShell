import type { AutomationEvent } from "../../events/automation-event.js";
import { resolveDotPath } from "./event-job-matcher.js";

/**
 * Context for template resolution at job fire time. Carries the matched
 * automation event so prompts and tool args can reference event fields and
 * payload paths.
 *
 * See tmp/plan/watch-to-agent/02-job-triggers.md §6.
 */
export interface TemplateContext {
  readonly event: {
    readonly type: string;
    readonly pluginId: string;
    readonly payload: Readonly<Record<string, unknown>>;
  };
  /** Phase E: accumulated pipeline context (outputKey → step output). */
  readonly context?: Readonly<Record<string, unknown>>;
}

/**
 * Build a TemplateContext from a matched AutomationEvent.
 */
export function templateContextFromEvent(event: AutomationEvent): TemplateContext {
  return {
    event: {
      type: event.eventType,
      pluginId: event.pluginId ?? "",
      payload: (event.payload ?? {}) as Readonly<Record<string, unknown>>,
    },
  };
}

const TEMPLATE_RE = /\{\{(event\.(type|pluginId)|payload\.[a-zA-Z0-9_.]+|context\.[a-zA-Z0-9_.]+)\}\}/g;

/**
 * Resolve `{{event.type}}`, `{{event.pluginId}}`, and `{{payload.*}}` templates
 * in a string against the given context.
 *
 * Rules (see 02-job-triggers.md §6):
 * 1. Dot-path only — no expression evaluation, no eval.
 * 2. Missing path → leave literal (including braces).
 * 3. Non-string values stringified with String(value).
 * 4. No whitespace inside braces — `{{ payload.x }}` is NOT resolved in v1.
 */
export function resolveTemplates(text: string, ctx: TemplateContext): string {
  return text.replace(TEMPLATE_RE, (match, expr: string) => {
    if (expr === "event.type") return ctx.event.type;
    if (expr === "event.pluginId") return ctx.event.pluginId;
    if (expr.startsWith("payload.")) {
      const path = expr.slice("payload.".length);
      const value = resolveDotPath(ctx.event.payload, path);
      if (value === undefined) return match; // leave literal
      return stringifyValue(value);
    }
    if (expr.startsWith("context.") && ctx.context) {
      const path = expr.slice("context.".length);
      const value = resolveDotPath(ctx.context, path);
      if (value === undefined) return match;
      return stringifyValue(value);
    }
    return match;
  });
}

/**
 * Resolve templates in all string values of a record (e.g. tool args).
 * Non-string values are passed through unchanged.
 */
export function resolveTemplatesInRecord(
  record: Readonly<Record<string, unknown>>,
  ctx: TemplateContext,
): Record<string, unknown> {
  const result: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(record)) {
    result[key] = typeof value === "string" ? resolveTemplates(value, ctx) : value;
  }
  return result;
}

function stringifyValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (value === null) return "null";
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}
