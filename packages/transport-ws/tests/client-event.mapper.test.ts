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

  it("carries streamSeq into the payload when the event has one", () => {
    expect(mapDomainEvent({
      type: "agent.text_delta",
      occurredAt: new Date("2026-07-29T00:00:00.000Z"),
      aggregateId: "trace-1",
      delta: "hi",
      streamSeq: 3,
    } as Parameters<typeof mapDomainEvent>[0] & { delta: string; streamSeq: number }, 7)).toEqual({
      kind: "event",
      event: "agent.text_delta",
      sequence: 7,
      payload: {
        traceId: "trace-1",
        delta: "hi",
        streamSeq: 3,
        timestamp: "2026-07-29T00:00:00.000Z",
      },
    });
  });

  it("omits streamSeq from the payload when the event does not carry one", () => {
    const envelope = mapDomainEvent({
      type: "plugin.installed",
      occurredAt: new Date("2026-07-29T00:00:00.000Z"),
      aggregateId: "notes",
      version: "1.0.0",
    } as Parameters<typeof mapDomainEvent>[0] & { version: string }, 2);
    expect(envelope?.payload).not.toHaveProperty("streamSeq");
  });

  it("maps agent.turn_started events", () => {
    expect(mapDomainEvent({
      type: "agent.turn_started",
      occurredAt: new Date("2026-07-29T00:00:00.000Z"),
      aggregateId: "trace-1",
      streamSeq: 1,
    } as Parameters<typeof mapDomainEvent>[0], 5)).toEqual({
      kind: "event",
      event: "agent.turn_started",
      sequence: 5,
      payload: {
        traceId: "trace-1",
        streamSeq: 1,
        timestamp: "2026-07-29T00:00:00.000Z",
      },
    });
  });

  it("maps agent.turn_end events with reason", () => {
    expect(mapDomainEvent({
      type: "agent.turn_end",
      occurredAt: new Date("2026-07-29T00:00:00.000Z"),
      aggregateId: "trace-1",
      reason: "cancelled",
      streamSeq: 10,
    } as Parameters<typeof mapDomainEvent>[0] & { reason: string }, 6)).toEqual({
      kind: "event",
      event: "agent.turn_end",
      sequence: 6,
      payload: {
        traceId: "trace-1",
        reason: "cancelled",
        streamSeq: 10,
        timestamp: "2026-07-29T00:00:00.000Z",
      },
    });
  });

  it("maps agent.turn_superseded events with byTraceId", () => {
    expect(mapDomainEvent({
      type: "agent.turn_superseded",
      occurredAt: new Date("2026-07-29T00:00:00.000Z"),
      aggregateId: "trace-old",
      byTraceId: "trace-new",
    } as Parameters<typeof mapDomainEvent>[0] & { byTraceId: string }, 7)).toEqual({
      kind: "event",
      event: "agent.turn_superseded",
      sequence: 7,
      payload: {
        traceId: "trace-old",
        byTraceId: "trace-new",
        timestamp: "2026-07-29T00:00:00.000Z",
      },
    });
  });

  it("maps agent.cancel_requested events", () => {
    expect(mapDomainEvent({
      type: "agent.cancel_requested",
      occurredAt: new Date("2026-07-29T00:00:00.000Z"),
      aggregateId: "trace-1",
    } as Parameters<typeof mapDomainEvent>[0], 8)).toEqual({
      kind: "event",
      event: "agent.cancel_requested",
      sequence: 8,
      payload: {
        traceId: "trace-1",
        timestamp: "2026-07-29T00:00:00.000Z",
      },
    });
  });
});
