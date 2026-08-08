import { describe, expect, it } from "vitest";
import { InMemoryActiveTurnProjection } from "../src/agent/services/in-memory-active-turn-projection.js";

describe("InMemoryActiveTurnProjection", () => {
  it("tracks sealed steps and streaming segment independently", () => {
    const proj = new InMemoryActiveTurnProjection();
    proj.start({ conversationId: "c1", traceId: "t1" });
    proj.setStreaming("c1", { kind: "reasoning", content: "Thinking…" });
    expect(proj.get("c1")?.streaming?.content).toBe("Thinking…");

    proj.setSteps("c1", [{ type: "reasoning", content: "Thinking full" }]);
    expect(proj.get("c1")?.steps).toHaveLength(1);
    expect(proj.get("c1")?.streaming).toBeUndefined();
  });

  it("clears only the matching traceId", () => {
    const proj = new InMemoryActiveTurnProjection();
    proj.start({ conversationId: "c1", traceId: "t1" });
    proj.clear("c1", "other");
    expect(proj.get("c1")?.traceId).toBe("t1");
    proj.clear("c1", "t1");
    expect(proj.get("c1")).toBeUndefined();
  });

  it("replaces an older active turn for the same conversation", () => {
    const proj = new InMemoryActiveTurnProjection();
    proj.start({ conversationId: "c1", traceId: "t1" });
    proj.start({ conversationId: "c1", traceId: "t2" });
    expect(proj.get("c1")?.traceId).toBe("t2");
    expect(proj.getByTraceId("t1")).toBeUndefined();
    expect(proj.getByTraceId("t2")?.conversationId).toBe("c1");
  });

  it("keeps the reserved assistant identity in the active snapshot", () => {
    const proj = new InMemoryActiveTurnProjection();
    proj.start({
      conversationId: "c1",
      traceId: "t1",
      messageId: "msg-assistant",
      messagePosition: 2,
    });

    expect(proj.get("c1")).toMatchObject({
      messageId: "msg-assistant",
      messagePosition: 2,
    });
  });

  it("tracks open tools until the next sealed step replace", () => {
    const proj = new InMemoryActiveTurnProjection();
    proj.start({ conversationId: "c1", traceId: "t1" });
    proj.openTool("c1", { id: "call-1", name: "ask_question", args: { question: "ok?" } });
    proj.endTool("c1", { id: "call-1", name: "ask_question", ok: true, result: { answer: "yes" } });
    expect(proj.get("c1")?.openTools[0]).toMatchObject({ id: "call-1", status: "ok" });
    proj.setSteps("c1", [{ type: "tool_calls", calls: [{ id: "call-1", name: "ask_question", ok: true }] }]);
    expect(proj.get("c1")?.openTools).toHaveLength(0);
  });

  it("keeps the provider-facing terminal projection for a completed live tool", () => {
    const proj = new InMemoryActiveTurnProjection();
    proj.start({ conversationId: "c1", traceId: "t1" });
    proj.openTool("c1", { id: "call-1", name: "todo", args: { action: "get" } });

    proj.endTool("c1", {
      id: "call-1",
      name: "todo",
      ok: true,
      result: { ok: true, items: [{ id: "task-1" }] },
      modelOutput: "status=success\n\nok=true\nitems[1]",
    });

    expect(proj.get("c1")?.openTools[0]?.output).toBe("status=success\n\nok=true\nitems[1]");
  });

  it("BH-AGENT-15 keeps independent mid-turn projections per conversation room", () => {
    const proj = new InMemoryActiveTurnProjection();
    proj.start({ conversationId: "room-a", traceId: "ta" });
    proj.start({ conversationId: "room-b", traceId: "tb" });
    proj.openTool("room-a", { id: "ca", name: "docs_search", args: { query: "a" } });
    proj.openTool("room-b", { id: "cb", name: "subagent", args: { prompt: "b" } });
    proj.setStreaming("room-a", { kind: "reasoning", content: "think A" });
    proj.setStreaming("room-b", { kind: "text", content: "text B" });

    expect(proj.get("room-a")).toMatchObject({
      traceId: "ta",
      streaming: { kind: "reasoning", content: "think A" },
      openTools: [{ id: "ca", name: "docs_search" }],
    });
    expect(proj.get("room-b")).toMatchObject({
      traceId: "tb",
      streaming: { kind: "text", content: "text B" },
      openTools: [{ id: "cb", name: "subagent" }],
    });

    proj.clear("room-a", "ta");
    expect(proj.get("room-a")).toBeUndefined();
    expect(proj.get("room-b")?.traceId).toBe("tb");
  });
});
