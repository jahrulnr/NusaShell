import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mkdtemp, rm, readFile, readdir, writeFile, mkdir } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { JsonlTelemetryWriter } from "../src/telemetry/jsonl-telemetry-writer.js";
import type { AgentTurnTelemetry, ProviderRequestTelemetry } from "@nusashell/application";

const REQUEST: ProviderRequestTelemetry = {
  kind: "provider_request",
  schemaVersion: 1,
  traceId: "trace-1",
  timestamp: "2026-08-06T10:00:00.000Z",
  providerId: "openrouter",
  model: "deepseek",
  round: 1,
  usage: {
    inputTokens: 100,
    outputTokens: 10,
    cachedInputTokens: 80,
    cacheWriteTokens: 0,
    reasoningOutputTokens: 2,
    source: "provider",
  },
  timing: { startedAt: "2026-08-06T10:00:00.000Z", completedAt: "2026-08-06T10:00:00.200Z", latencyMs: 200 },
  outcome: { status: "completed", finishReason: "stop" },
};

const TURN: AgentTurnTelemetry = {
  kind: "agent_turn",
  schemaVersion: 1,
  traceId: "trace-1",
  conversationId: "conv-1",
  startedAt: "2026-08-06T10:00:00.000Z",
  completedAt: "2026-08-06T10:00:02.000Z",
  durationMs: 2000,
  providerId: "openrouter",
  model: "deepseek",
  status: "completed",
  rounds: 2,
  tools: { calls: 1, succeeded: 1, failed: 0 },
  compaction: { count: 0 },
  usage: {
    inputTokens: 100,
    outputTokens: 10,
    cachedInputTokens: 80,
    cacheWriteTokens: 0,
    reasoningOutputTokens: 2,
    source: "provider",
  },
};

describe("JsonlTelemetryWriter", () => {
  let dir: string;

  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), "telemetry-"));
  });

  afterEach(async () => {
    await rm(dir, { recursive: true, force: true });
  });

  it("appends provider requests and turns to separate daily files", async () => {
    const now = () => new Date("2026-08-06T10:00:00.000Z");
    const writer = new JsonlTelemetryWriter({ dir, now });
    writer.recordProviderRequest(REQUEST);
    writer.recordProviderRequest({ ...REQUEST, round: 2 });
    writer.recordTurn(TURN);
    await writer.flush();

    const requestsFile = join(dir, "provider-requests-2026-08-06.jsonl");
    const turnsFile = join(dir, "agent-turns-2026-08-06.jsonl");

    const requestLines = (await readFile(requestsFile, "utf8")).trim().split("\n");
    expect(requestLines).toHaveLength(2);
    expect(JSON.parse(requestLines[0]!)).toMatchObject({ kind: "provider_request", round: 1 });
    expect(JSON.parse(requestLines[1]!)).toMatchObject({ round: 2 });

    const turnLines = (await readFile(turnsFile, "utf8")).trim().split("\n");
    expect(turnLines).toHaveLength(1);
    expect(JSON.parse(turnLines[0]!)).toMatchObject({ kind: "agent_turn", traceId: "trace-1" });
  });

  it("rotates files by UTC day", async () => {
    let day = "2026-08-06T23:59:59.000Z";
    const writer = new JsonlTelemetryWriter({ dir, now: () => new Date(day) });
    writer.recordTurn(TURN);
    await writer.flush();
    day = "2026-08-07T00:00:01.000Z";
    writer.recordTurn(TURN);
    await writer.flush();
    const files = (await readdir(dir)).filter((name) => name.startsWith("agent-turns-")).sort();
    expect(files).toEqual(["agent-turns-2026-08-06.jsonl", "agent-turns-2026-08-07.jsonl"]);
  });

  it("prunes daily files older than the retention window", async () => {
    await mkdir(dir, { recursive: true });
    await writeFile(join(dir, "agent-turns-2026-06-01.jsonl"), "{}\n", "utf8");
    await writeFile(join(dir, "provider-requests-2026-06-01.jsonl"), "{}\n", "utf8");
    const writer = new JsonlTelemetryWriter({ dir, retentionDays: 30, now: () => new Date("2026-08-06T10:00:00.000Z") });
    writer.recordTurn(TURN);
    await writer.flush();
    const files = await readdir(dir);
    expect(files).not.toContain("agent-turns-2026-06-01.jsonl");
    expect(files).not.toContain("provider-requests-2026-06-01.jsonl");
    expect(files).toContain("agent-turns-2026-08-06.jsonl");
  });

  it("reports write failures via onError instead of throwing", async () => {
    const errors: unknown[] = [];
    // Point at a path whose parent is a file to force a mkdir/append failure.
    const filePath = join(dir, "not-a-dir");
    await writeFile(filePath, "x", "utf8");
    const writer = new JsonlTelemetryWriter({ dir: join(filePath, "telemetry"), onError: (error) => errors.push(error) });
    expect(() => writer.recordTurn(TURN)).not.toThrow();
    await writer.flush();
    expect(errors.length).toBeGreaterThan(0);
  });
});
