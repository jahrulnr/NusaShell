import { describe, expect, it } from "vitest";
import {
  parseSchedule,
  computeNextRun,
  describeSchedule,
  ScheduleParseError,
} from "../src/job/schedule-parser.js";
import { recurringCatchupGraceSeconds, ONCE_GRACE_SECONDS } from "../src/job/job-model.js";

const NOW = new Date("2025-01-01T00:00:00Z");

describe("parseSchedule", () => {
  it("parses 'every 30m'", () => {
    expect(parseSchedule("every 30m", NOW)).toEqual({ kind: "interval", minutes: 30 });
  });

  it("parses bare '30m'", () => {
    expect(parseSchedule("30m", NOW)).toEqual({ kind: "interval", minutes: 30 });
  });

  it("parses '2h'", () => {
    expect(parseSchedule("2h", NOW)).toEqual({ kind: "interval", minutes: 120 });
  });

  it("parses '1d'", () => {
    expect(parseSchedule("1d", NOW)).toEqual({ kind: "interval", minutes: 1440 });
  });

  it("parses 5-field cron", () => {
    expect(parseSchedule("0 9 * * *", NOW)).toEqual({ kind: "cron", expr: "0 9 * * *" });
  });

  it("parses ISO timestamp as once", () => {
    const future = new Date(NOW.getTime() + 60_000).toISOString();
    expect(parseSchedule(future, NOW)).toEqual({ kind: "once", runAt: future });
  });

  it("parses date-only as once at 00:00Z", () => {
    const future = "2025-01-02";
    expect(parseSchedule(future, NOW)).toEqual({ kind: "once", runAt: "2025-01-02T00:00:00.000Z" });
  });

  it("rejects past one-shot beyond grace", () => {
    const past = new Date(NOW.getTime() - (ONCE_GRACE_SECONDS + 10) * 1000).toISOString();
    expect(() => parseSchedule(past, NOW)).toThrow(ScheduleParseError);
  });

  it("accepts past one-shot within grace", () => {
    const past = new Date(NOW.getTime() - 30 * 1000).toISOString();
    expect(parseSchedule(past, NOW)).toEqual({ kind: "once", runAt: past });
  });

  it("rejects empty input", () => {
    expect(() => parseSchedule("   ", NOW)).toThrow(ScheduleParseError);
  });

  it("rejects garbage", () => {
    expect(() => parseSchedule("banana", NOW)).toThrow(ScheduleParseError);
  });

  it("rejects cron with wrong field count", () => {
    expect(() => parseSchedule("0 9 * *", NOW)).toThrow(ScheduleParseError);
  });
});

describe("computeNextRun", () => {
  it("returns null for once", () => {
    expect(computeNextRun({ kind: "once", runAt: "2025-01-01T00:00:00Z" }, null, NOW)).toBeNull();
  });

  it("interval advances by minutes from lastRunAt", () => {
    const lastRun = new Date("2025-01-01T00:00:00Z").toISOString();
    const next = computeNextRun({ kind: "interval", minutes: 30 }, lastRun, NOW);
    expect(next).toBe("2025-01-01T00:30:00.000Z");
  });

  it("interval fast-forwards past due slots", () => {
    const lastRun = new Date("2024-12-31T23:00:00Z").toISOString();
    const next = computeNextRun({ kind: "interval", minutes: 30 }, lastRun, NOW);
    // NOW is 2025-01-01T00:00:00Z; next must be > NOW
    expect(next).not.toBeNull();
    expect(new Date(next!).getTime()).toBeGreaterThan(NOW.getTime());
  });

  it("interval from null lastRunAt anchors on now", () => {
    const next = computeNextRun({ kind: "interval", minutes: 30 }, null, NOW);
    expect(next).toBe("2025-01-01T00:30:00.000Z");
  });

  it("cron finds next hit after now", () => {
    // 0 9 * * * -> next 09:00 UTC on 2025-01-01
    const next = computeNextRun({ kind: "cron", expr: "0 9 * * *" }, null, NOW);
    expect(next).toBe("2025-01-01T09:00:00.000Z");
  });

  it("cron anchored on lastRunAt finds the following hit", () => {
    const lastRun = new Date("2025-01-01T09:00:00Z").toISOString();
    const next = computeNextRun({ kind: "cron", expr: "0 9 * * *" }, lastRun, NOW);
    expect(next).toBe("2025-01-02T09:00:00.000Z");
  });
});

describe("describeSchedule", () => {
  it("describes once", () => {
    expect(describeSchedule({ kind: "once", runAt: "2025-01-01T00:00:00Z" })).toBe(
      "once at 2025-01-01T00:00:00Z",
    );
  });

  it("describes interval in hours", () => {
    expect(describeSchedule({ kind: "interval", minutes: 120 })).toBe("every 2h");
  });

  it("describes interval in days", () => {
    expect(describeSchedule({ kind: "interval", minutes: 1440 })).toBe("every 1d");
  });

  it("describes cron", () => {
    expect(describeSchedule({ kind: "cron", expr: "0 9 * * *" })).toBe("cron 0 9 * * *");
  });
});

describe("recurringCatchupGraceSeconds", () => {
  it("is at least the one-shot grace", () => {
    expect(recurringCatchupGraceSeconds(1)).toBe(ONCE_GRACE_SECONDS);
  });

  it("is half the period for medium intervals", () => {
    // 60min period -> 30min = 1800s
    expect(recurringCatchupGraceSeconds(60)).toBe(1800);
  });

  it("caps at 2h for long intervals", () => {
    expect(recurringCatchupGraceSeconds(24 * 60)).toBe(2 * 60 * 60);
  });
});
