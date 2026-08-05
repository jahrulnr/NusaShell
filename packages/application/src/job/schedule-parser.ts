/**
 * Schedule parsing + next-run computation for jobs (pure, no I/O).
 *
 * Grammar accepted by `parseSchedule`:
 *   - "every 30m" / "every 2h" / "every 1d"
 *   - "30m" / "2h" / "1d"
 *   - "0 9 * * *"  (5-field cron)
 *   - ISO timestamp  -> once
 *
 * A `once` schedule whose runAt is in the past beyond the 120s grace window
 * is rejected at parse time.
 */
import {
  type JobSchedule,
  ONCE_GRACE_SECONDS,
} from "./job-model.js";

export class ScheduleParseError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ScheduleParseError";
  }
}

const INTERVAL_RE = /^(?:every\s+)?(\d+)\s*([mhd])$/i;

export function parseSchedule(
  input: string,
  now: Date = new Date(),
): JobSchedule {
  const trimmed = input.trim();
  if (trimmed.length === 0) throw new ScheduleParseError("schedule is empty");

  // ISO timestamp -> once (accepts date-only, with/without time, with/without ms, with/without Z)
  if (/^\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2}(:\d{2})?(\.\d{1,3})?Z?)?$/i.test(trimmed)) {
    const iso = normalizeIso(trimmed);
    const parsed = new Date(iso);
    if (Number.isNaN(parsed.getTime())) {
      throw new ScheduleParseError(`invalid timestamp: ${trimmed}`);
    }
    const ageSeconds = (now.getTime() - parsed.getTime()) / 1000;
    if (ageSeconds > ONCE_GRACE_SECONDS) {
      throw new ScheduleParseError(
        `one-shot time is in the past beyond the ${ONCE_GRACE_SECONDS}s grace window`,
      );
    }
    return { kind: "once", runAt: parsed.toISOString() };
  }

  // interval shorthand
  const intervalMatch = INTERVAL_RE.exec(trimmed);
  if (intervalMatch) {
    const value = parseInt(intervalMatch[1]!, 10);
    const unit = intervalMatch[2]!.toLowerCase();
    const minutes = value * (unit === "m" ? 1 : unit === "h" ? 60 : 1440);
    if (minutes <= 0) throw new ScheduleParseError("interval must be positive");
    return { kind: "interval", minutes };
  }

  // 5-field cron
  if (trimmed.split(/\s+/).length === 5) {
    validateCron(trimmed);
    return { kind: "cron", expr: trimmed };
  }

  throw new ScheduleParseError(`unrecognized schedule: ${trimmed}`);
}

function normalizeIso(value: string): string {
  // Accept "2025-01-01" or "2025-01-01 09:00" -> ISO (with optional ms/Z)
  if (/^\d{4}-\d{2}-\d{2}$/.test(value)) return `${value}T00:00:00Z`;
  if (/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/.test(value)) return `${value.replace(" ", "T")}:00Z`;
  if (/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/.test(value)) return `${value.replace(" ", "T")}Z`;
  if (/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value)) return `${value}:00Z`;
  // Already full ISO (with or without ms/Z) — Date parses it natively.
  return value;
}

/**
 * Compute the next run time for a schedule, anchored on the last run (or now).
 * Returns null when a one-shot has already fired (no next run).
 */
export function computeNextRun(
  schedule: JobSchedule,
  lastRunAt: string | null,
  now: Date = new Date(),
): string | null {
  switch (schedule.kind) {
    case "once":
      return null;
    case "interval": {
      const base = lastRunAt ? new Date(lastRunAt).getTime() : now.getTime();
      const ms = schedule.minutes * 60_000;
      let next = base + ms;
      // Fast-forward past due slots without firing them all (at-most-once).
      while (next <= now.getTime()) next += ms;
      return new Date(next).toISOString();
    }
    case "cron": {
      const base = lastRunAt ? new Date(lastRunAt) : now;
      const hit = nextCronHit(schedule.expr, base, now);
      return hit ? hit.toISOString() : null;
    }
  }
}

// ---- 5-field cron matcher (dependency-free) ----

interface CronFields {
  readonly minute: readonly number[];
  readonly hour: readonly number[];
  readonly dom: readonly number[];
  readonly month: readonly number[];
  readonly dow: readonly number[];
}

const FIELD_RANGES: ReadonlyArray<readonly [number, number]> = [
  [0, 59], // minute
  [0, 23], // hour
  [1, 31], // dom
  [1, 12], // month
  [0, 6], // dow (0=Sun)
];

function validateCron(expr: string): void {
  const parts = expr.split(/\s+/);
  if (parts.length !== 5) throw new ScheduleParseError(`cron must have 5 fields: ${expr}`);
  for (let i = 0; i < 5; i += 1) {
    try {
      parseField(parts[i]!, FIELD_RANGES[i]![0], FIELD_RANGES[i]![1]);
    } catch (error) {
      throw new ScheduleParseError(
        `cron field ${i + 1} invalid: ${error instanceof Error ? error.message : String(error)}`,
      );
    }
  }
}

function parseField(field: string, min: number, max: number): number[] {
  if (field === "*") return range(min, max);
  const out = new Set<number>();
  for (const part of field.split(",")) {
    if (part.includes("/")) {
      const [base, stepStr] = part.split("/");
      const step = parseInt(stepStr!, 10);
      if (!Number.isInteger(step) || step <= 0) throw new Error(`bad step: ${stepStr}`);
      const [start, end] = parseRange(base!, min, max);
      for (let v = start; v <= end; v += step) out.add(v);
    } else {
      const [start, end] = parseRange(part, min, max);
      for (let v = start; v <= end; v += 1) out.add(v);
    }
  }
  return [...out].sort((a, b) => a - b);
}

function parseRange(token: string, min: number, max: number): [number, number] {
  if (token === "*") return [min, max];
  const check = (v: number): number => {
    if (v < min || v > max) throw new Error(`value out of range ${min}-${max}: ${v}`);
    return v;
  };
  if (token.includes("-")) {
    const [a, b] = token.split("-");
    const start = parseInt(a!, 10);
    const end = parseInt(b!, 10);
    if (!Number.isInteger(start) || !Number.isInteger(end)) throw new Error(`bad range: ${token}`);
    check(start);
    check(end);
    if (start > end) throw new Error(`inverted range: ${token}`);
    return [start, end];
  }
  const v = parseInt(token, 10);
  if (!Number.isInteger(v)) throw new Error(`bad value: ${token}`);
  check(v);
  return [v, v];
}

function range(min: number, max: number): number[] {
  const out: number[] = [];
  for (let v = min; v <= max; v += 1) out.push(v);
  return out;
}

function parseCron(expr: string): CronFields {
  const parts = expr.split(/\s+/);
  return {
    minute: parseField(parts[0]!, 0, 59),
    hour: parseField(parts[1]!, 0, 23),
    dom: parseField(parts[2]!, 1, 31),
    month: parseField(parts[3]!, 1, 12),
    dow: parseField(parts[4]!, 0, 6),
  };
}

const FULL_DOM_LEN = 31; // FIELD_RANGES dom: 1..31
const FULL_DOW_LEN = 7; // FIELD_RANGES dow: 0..6

function matchesCron(fields: CronFields, d: Date): boolean {
  // Vixie/standard cron: when BOTH day-of-month and day-of-week are
  // restricted (not "*"), a match on EITHER satisfies the day condition.
  // When one is "*", the restricted field alone governs.
  const domRestricted = fields.dom.length !== FULL_DOM_LEN;
  const dowRestricted = fields.dow.length !== FULL_DOW_LEN;
  const domHit = fields.dom.includes(d.getUTCDate());
  const dowHit = fields.dow.includes(d.getUTCDay());
  const dayOk =
    domRestricted && dowRestricted ? domHit || dowHit : domHit && dowHit;
  return (
    fields.minute.includes(d.getUTCMinutes()) &&
    fields.hour.includes(d.getUTCHours()) &&
    fields.month.includes(d.getUTCMonth() + 1) &&
    dayOk
  );
}

/**
 * Find the next time that matches the cron expression. When `lastRunAt` is
 * provided (base), the result is strictly AFTER base; otherwise it is >= now.
 * Searches minute-by-minute up to 366 days.
 */
function nextCronHit(expr: string, base: Date, now: Date): Date | null {
  const fields = parseCron(expr);
  // Start from the latest of base+1min (so we don't re-fire the same slot) and now.
  const startMs = Math.max(base.getTime() + 60_000, now.getTime());
  // Begin at the next minute boundary.
  let cursor = new Date(Math.ceil(startMs / 60_000) * 60_000);
  const limit = cursor.getTime() + 366 * 24 * 60 * 60_000;
  while (cursor.getTime() <= limit) {
    if (matchesCron(fields, cursor)) return cursor;
    cursor = new Date(cursor.getTime() + 60_000);
  }
  return null;
}

/** Human-readable schedule label for the UI. */
export function describeSchedule(schedule: JobSchedule): string {
  switch (schedule.kind) {
    case "once":
      return `once at ${schedule.runAt}`;
    case "interval": {
      const m = schedule.minutes;
      if (m % 1440 === 0) return `every ${m / 1440}d`;
      if (m % 60 === 0) return `every ${m / 60}h`;
      return `every ${m}m`;
    }
    case "cron":
      return `cron ${schedule.expr}`;
  }
}
