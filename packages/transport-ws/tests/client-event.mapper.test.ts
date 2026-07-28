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
});
