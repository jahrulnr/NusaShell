import { describe, expect, it } from "vitest";
import {
  RunAgentTurnHandler,
  TelemetryAgentProvider,
  buildTurnTelemetry,
  cacheHitRate,
  freshInputTokens,
  toTelemetryUsage,
  withTelemetry,
  type AgentProvider,
  type AgentProviderRequest,
  type AgentProviderResult,
  type AgentToolGateway,
  type AgentTokenUsage,
  type AgentTurnTelemetry,
  type ProviderRequestTelemetry,
  type TelemetryPort,
} from "../src/index.js";

const USAGE: AgentTokenUsage = {
  inputTokens: 100,
  outputTokens: 20,
  cachedInputTokens: 80,
  cacheWriteTokens: 0,
  reasoningOutputTokens: 5,
};

class RecordingTelemetry implements TelemetryPort {
  readonly requests: ProviderRequestTelemetry[] = [];
  readonly turns: AgentTurnTelemetry[] = [];
  recordProviderRequest(record: ProviderRequestTelemetry): void {
    this.requests.push(record);
  }
  recordTurn(record: AgentTurnTelemetry): void {
    this.turns.push(record);
  }
}

function makeClock(times: number[]) {
  let index = 0;
  return { now: () => times[Math.min(index++, times.length - 1)]! };
}

describe("telemetry usage helpers", () => {
  it("derives fresh input and cache hit rate", () => {
    const usage = toTelemetryUsage(USAGE);
    expect(usage.source).toBe("provider");
    expect(freshInputTokens(usage)).toBe(20);
    expect(cacheHitRate(usage)).toBeCloseTo(0.8);
  });

  it("returns a zero cache hit rate when there is no input", () => {
    expect(cacheHitRate(toTelemetryUsage({ ...USAGE, inputTokens: 0, cachedInputTokens: 0 }))).toBe(0);
  });
});

describe("buildTurnTelemetry", () => {
  it("aggregates rounds, tool outcomes, compaction, and usage", () => {
    const record = buildTurnTelemetry({
      traceId: "t1",
      conversationId: "c1",
      status: "completed",
      startedAtMs: 1000,
      completedAtMs: 4000,
      rounds: 3,
      toolCalls: [{ ok: true }, { ok: false }, { ok: true }],
      hasCompaction: true,
      model: "m",
      providerId: "p",
      usage: USAGE,
    });
    expect(record).toMatchObject({
      kind: "agent_turn",
      schemaVersion: 1,
      traceId: "t1",
      conversationId: "c1",
      status: "completed",
      rounds: 3,
      durationMs: 3000,
      tools: { calls: 3, succeeded: 2, failed: 1 },
      compaction: { count: 1 },
    });
    expect(record.usage).toMatchObject({ inputTokens: 100, cachedInputTokens: 80, source: "provider" });
  });

  it("omits usage and optional ids when absent", () => {
    const record = buildTurnTelemetry({
      traceId: "t2",
      status: "failed",
      startedAtMs: 0,
      completedAtMs: 0,
      rounds: 0,
      toolCalls: [],
      hasCompaction: false,
    });
    expect(record.usage).toBeUndefined();
    expect(record.conversationId).toBeUndefined();
    expect(record.compaction.count).toBe(0);
    expect(record.durationMs).toBe(0);
  });
});

describe("TelemetryAgentProvider", () => {
  it("records a completed request with usage, timing, and finish reason", async () => {
    const telemetry = new RecordingTelemetry();
    const inner: AgentProvider = {
      id: "openrouter",
      managesAttemptBudget: true,
      async complete(): Promise<AgentProviderResult> {
        return { text: "hi", status: "stop", model: "deepseek", providerId: "openrouter", usage: USAGE };
      },
    };
    const provider = new TelemetryAgentProvider(inner, telemetry, makeClock([1000, 1150]));
    expect(provider.id).toBe("openrouter");
    expect(provider.managesAttemptBudget).toBe(true);

    const request: AgentProviderRequest = { traceId: "trace-1", round: 2, messages: [], tools: [] };
    const result = await provider.complete(request);
    expect(result.text).toBe("hi");

    expect(telemetry.requests).toHaveLength(1);
    const record = telemetry.requests[0]!;
    expect(record).toMatchObject({
      kind: "provider_request",
      traceId: "trace-1",
      round: 2,
      providerId: "openrouter",
      model: "deepseek",
      outcome: { status: "completed", finishReason: "stop" },
    });
    expect(record.timing.latencyMs).toBe(150);
    expect(record.usage).toMatchObject({ inputTokens: 100, cachedInputTokens: 80, source: "provider" });
  });

  it("records a failed request with an error code and rethrows", async () => {
    const telemetry = new RecordingTelemetry();
    const inner: AgentProvider = {
      id: "p1",
      async complete(): Promise<AgentProviderResult> {
        throw Object.assign(new Error("boom"), { code: "AGENT_PROVIDER_FAILED" });
      },
    };
    const provider = new TelemetryAgentProvider(inner, telemetry, makeClock([0, 42]));
    await expect(provider.complete({ traceId: "t", round: 1, messages: [], tools: [] })).rejects.toThrow("boom");
    expect(telemetry.requests).toHaveLength(1);
    expect(telemetry.requests[0]).toMatchObject({
      outcome: { status: "failed", errorCode: "AGENT_PROVIDER_FAILED" },
      round: 1,
    });
    expect(telemetry.requests[0]!.timing.latencyMs).toBe(42);
  });

  it("never lets a throwing sink break the provider call", async () => {
    const throwing: TelemetryPort = {
      recordProviderRequest() { throw new Error("sink down"); },
      recordTurn() { throw new Error("sink down"); },
    };
    const inner: AgentProvider = { id: "p", async complete() { return { text: "ok" }; } };
    const provider = new TelemetryAgentProvider(inner, throwing);
    await expect(provider.complete({ traceId: "t", round: 1, messages: [], tools: [] })).resolves.toMatchObject({ text: "ok" });
  });

  it("withTelemetry returns the original provider when no sink is configured", () => {
    const inner: AgentProvider = { id: "p", async complete() { return {}; } };
    expect(withTelemetry(inner, undefined)).toBe(inner);
    expect(withTelemetry(inner, new RecordingTelemetry())).not.toBe(inner);
  });
});

// --- Handler wiring ---

class ScriptedProvider implements AgentProvider {
  readonly id = "scripted";
  private n = 0;
  constructor(private readonly responses: readonly AgentProviderResult[]) {}
  async complete(): Promise<AgentProviderResult> {
    const response = this.responses[this.n++];
    if (!response) throw new Error("No scripted provider response");
    return response;
  }
}

class FakeToolGateway implements AgentToolGateway {
  beginTurn() {}
  endTurn() {}
  cancelTurn() {}
  async listTools() { return []; }
  async execute() { return { ok: true }; }
}

function makeRegistry(provider: AgentProvider) {
  return { get: (id: string) => (id === provider.id ? provider : undefined), list: () => [provider] };
}

const RUNTIME = {
  strategy: "failover" as const,
  totalAttemptBudget: 1,
  maxToolRounds: 1,
  maxRepeatedToolCalls: 1,
  softRecoverAttempts: 0,
  maxConcurrentToolCalls: 1,
};

function makeHandler(provider: AgentProvider, telemetry: TelemetryPort, now: () => number) {
  return new RunAgentTurnHandler(
    makeRegistry(provider),
    new FakeToolGateway(),
    "scripted",
    RUNTIME,
    undefined, undefined, undefined, undefined, undefined, undefined, undefined,
    undefined, undefined, undefined, undefined, undefined, undefined, undefined,
    undefined, undefined, undefined, undefined, undefined, undefined,
    { telemetry, now },
  );
}

describe("RunAgentTurnHandler telemetry", () => {
  it("records a completed turn aggregate with usage and duration", async () => {
    const telemetry = new RecordingTelemetry();
    const provider = new ScriptedProvider([{ text: "done", model: "m1", providerId: "scripted", usage: USAGE }]);
    const handler = makeHandler(provider, telemetry, makeClock([1000, 2500]).now);
    await handler.handle({
      kind: "run-agent-turn",
      traceId: "trace-telemetry",
      conversationId: "conv-x",
      messages: [{ role: "user", content: "hi" }],
      pluginIds: [],
    });
    expect(telemetry.turns).toHaveLength(1);
    expect(telemetry.turns[0]).toMatchObject({
      kind: "agent_turn",
      traceId: "trace-telemetry",
      conversationId: "conv-x",
      status: "completed",
      rounds: 1,
      durationMs: 1500,
    });
    expect(telemetry.turns[0]!.usage).toMatchObject({ inputTokens: 100, cachedInputTokens: 80 });
  });

  it("records a failed turn aggregate when the provider throws", async () => {
    const telemetry = new RecordingTelemetry();
    const provider = new ScriptedProvider([]);
    const handler = makeHandler(provider, telemetry, makeClock([0, 10]).now);
    await expect(handler.handle({
      kind: "run-agent-turn",
      traceId: "trace-fail-telemetry",
      messages: [{ role: "user", content: "hi" }],
      pluginIds: [],
    })).rejects.toThrow();
    expect(telemetry.turns).toHaveLength(1);
    expect(telemetry.turns[0]).toMatchObject({ traceId: "trace-fail-telemetry", status: "failed" });
  });
});
