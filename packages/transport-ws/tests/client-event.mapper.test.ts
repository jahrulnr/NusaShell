import { describe, expect, it } from "vitest";
import { mapDomainEvent } from "../src/index.js";

describe("mapDomainEvent", () => {
  it("maps an agent text delta without treating it as durable conversation state", () => {
    expect(mapDomainEvent({
      type: "agent.text_delta",
      occurredAt: new Date("2026-07-29T00:00:00.000Z"),
      aggregateId: "trace-1",
      delta: "hello",
    } as Parameters<typeof mapDomainEvent>[0] & { delta: string }, 7)).toEqual({
      kind: "event",
      event: "agent.text_delta",
      sequence: 7,
      payload: {
        traceId: "trace-1",
        delta: "hello",
        timestamp: "2026-07-29T00:00:00.000Z",
      },
    });
  });

  it("maps tool call end events with truncated output previews", () => {
    expect(mapDomainEvent({
      type: "agent.tool_call_end",
      occurredAt: new Date("2026-07-29T00:00:00.000Z"),
      aggregateId: "trace-1",
      execution: {
        id: "call-1",
        name: "docs_list",
        ok: true,
        args: { limit: 2 },
        result: { docs: ["a.md", "b.md"] },
      },
    } as Parameters<typeof mapDomainEvent>[0], 3)).toEqual({
      kind: "event",
      event: "agent.tool_call_end",
      sequence: 3,
      payload: {
        traceId: "trace-1",
        callId: "call-1",
        name: "docs_list",
        ok: true,
        args: { limit: 2 },
        output: "{\n  \"docs\": [\n    \"a.md\",\n    \"b.md\"\n  ]\n}",
        timestamp: "2026-07-29T00:00:00.000Z",
      },
    });
  });

  it("maps live context estimates during an agent turn", () => {
    expect(mapDomainEvent({
      type: "agent.context",
      occurredAt: new Date("2026-07-29T00:00:00.000Z"),
      aggregateId: "trace-1",
      estimatedTokens: 1280,
      usage: {
        inputTokens: 1200,
        outputTokens: 80,
        cachedInputTokens: 0,
        cacheWriteTokens: 0,
        reasoningOutputTokens: 0,
      },
    } as Parameters<typeof mapDomainEvent>[0], 4)).toEqual({
      kind: "event",
      event: "agent.context",
      sequence: 4,
      payload: {
        traceId: "trace-1",
        estimatedTokens: 1280,
        inputTokens: 1200,
        outputTokens: 80,
        timestamp: "2026-07-29T00:00:00.000Z",
      },
    });
  });

  it("maps agent.learning_updated events", () => {
    expect(mapDomainEvent({
      type: "agent.learning_updated",
      occurredAt: new Date("2026-07-29T00:00:00.000Z"),
      aggregateId: "review-trace-1",
      kinds: ["memory", "skills"],
      summary: "Updated memory and skills",
      reviewTraceId: "review-trace-1",
    } as Parameters<typeof mapDomainEvent>[0] & {
      kinds: string[];
      summary: string;
      reviewTraceId: string;
    }, 9)).toEqual({
      kind: "event",
      event: "agent.learning_updated",
      sequence: 9,
      payload: {
        reviewTraceId: "review-trace-1",
        kinds: ["memory", "skills"],
        summary: "Updated memory and skills",
        timestamp: "2026-07-29T00:00:00.000Z",
      },
    });
  });
});
