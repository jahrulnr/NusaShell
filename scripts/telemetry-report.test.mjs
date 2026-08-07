import { describe, expect, it } from "vitest";
import {
  computeReport,
  parseJsonl,
  partitionRecords,
  turnsToCsv,
} from "./telemetry-report.mjs";

function usage(input, cached, output = 0, reasoning = 0) {
  return { inputTokens: input, cachedInputTokens: cached, outputTokens: output, reasoningOutputTokens: reasoning, source: "provider" };
}

function providerRequest(traceId, round, u) {
  return { kind: "provider_request", schemaVersion: 1, traceId, round, usage: u, timing: { latencyMs: 1 }, outcome: { status: "completed" } };
}

function turn(traceId, status, rounds, u, tools = { calls: 0, succeeded: 0, failed: 0 }) {
  return { kind: "agent_turn", schemaVersion: 1, traceId, status, rounds, tools, compaction: { count: 0 }, usage: u, durationMs: 100 };
}

describe("parseJsonl", () => {
  it("parses valid lines and skips blank/corrupt lines", () => {
    const text = '{"a":1}\n\n  \nnot json\n{"b":2}\n';
    expect(parseJsonl(text)).toEqual([{ a: 1 }, { b: 2 }]);
  });
});

describe("partitionRecords", () => {
  it("splits provider requests from turns", () => {
    const { providerRequests, turns } = partitionRecords([
      providerRequest("t1", 1, usage(10, 5)),
      turn("t1", "completed", 1, usage(10, 5)),
      { kind: "unknown" },
    ]);
    expect(providerRequests).toHaveLength(1);
    expect(turns).toHaveLength(1);
  });
});

describe("computeReport", () => {
  it("computes cache hit rate and fresh tokens from provider requests", () => {
    const report = computeReport({
      providerRequests: [
        providerRequest("t1", 1, usage(100, 80, 10)),
        providerRequest("t1", 2, usage(100, 90, 5)),
      ],
      turns: [turn("t1", "completed", 2, usage(200, 170, 15))],
    });
    expect(report.providerRequests).toBe(2);
    expect(report.turns).toBe(1);
    expect(report.tokens.inputTokens).toBe(200);
    expect(report.tokens.cachedInputTokens).toBe(170);
    expect(report.tokens.freshInputTokens).toBe(30);
    expect(report.cacheHitRate).toBeCloseTo(0.85);
    expect(report.freshTokenRatio).toBeCloseTo(0.15);
    expect(report.providerRequestsPerTurn).toBe(2);
    expect(report.freshTokensPerCompletedTurn).toBe(30);
  });

  it("computes failure waste ratio from non-completed turn tokens", () => {
    const report = computeReport({
      providerRequests: [],
      turns: [
        turn("t1", "completed", 1, usage(100, 0, 20)), // 120 tokens
        turn("t2", "failed", 1, usage(60, 0, 0)),      // 60 tokens
      ],
    });
    // 60 wasted / 180 total
    expect(report.failureWasteRatio).toBeCloseTo(60 / 180);
    expect(report.turnsByStatus).toMatchObject({ completed: 1, failed: 1 });
  });

  it("reports per-trace provider request percentiles", () => {
    const report = computeReport({
      providerRequests: [
        providerRequest("a", 1, usage(1, 0)),
        providerRequest("a", 2, usage(1, 0)),
        providerRequest("b", 1, usage(1, 0)),
      ],
      turns: [turn("a", "completed", 2, usage(2, 0)), turn("b", "completed", 1, usage(1, 0))],
    });
    expect(report.providerRequestsPerTraceMedian).toBeCloseTo(1.5);
    expect(report.costPerCompletedTurn).toBeNull();
  });

  it("handles empty input without dividing by zero", () => {
    const report = computeReport({ providerRequests: [], turns: [] });
    expect(report.cacheHitRate).toBe(0);
    expect(report.providerRequestsPerTurn).toBe(0);
    expect(report.failureWasteRatio).toBe(0);
  });
});

describe("turnsToCsv", () => {
  it("emits a header and one row per turn", () => {
    const csv = turnsToCsv([turn("t1", "completed", 2, usage(100, 80, 10, 3), { calls: 2, succeeded: 1, failed: 1 })]);
    const lines = csv.split("\n");
    expect(lines[0]).toContain("traceId");
    expect(lines[0]).toContain("freshInputTokens");
    expect(lines).toHaveLength(2);
    expect(lines[1]).toContain("t1");
    expect(lines[1]).toContain("completed");
    // fresh = 100 - 80 = 20
    expect(lines[1].split(",")).toContain("20");
  });
});
