import { describe, expect, it } from "vitest";
import { createSubagentRunStartedEvent } from "../src/agent/events/subagent-run-started.event.js";
import type { SubagentRunStartedEvent } from "../src/agent/events/subagent-run-started.event.js";
import { createSubagentRunEndedEvent } from "../src/agent/events/subagent-run-ended.event.js";
import type { SubagentRunEndedEvent } from "../src/agent/events/subagent-run-ended.event.js";

describe("subagent events", () => {
  it("creates a run_started event with required fields", () => {
    const event = createSubagentRunStartedEvent("run-1", "conv-1", "cursor", "Refactor auth");
    expect(event.type).toBe("subagent.run_started");
    expect(event.runId).toBe("run-1");
    expect(event.conversationId).toBe("conv-1");
    expect(event.providerId).toBe("cursor");
    expect(event.prompt).toBe("Refactor auth");
    expect(event.aggregateId).toBe("run-1");
    expect(event.occurredAt).toBeInstanceOf(Date);
  });

  it("creates a run_started event with optional fields", () => {
    const event = createSubagentRunStartedEvent("run-2", "conv-2", "codex", "Fix bug", {
      title: "Bug fix",
      parentConversationId: "parent-conv",
      parentTraceId: "parent-trace",
    });
    expect(event.title).toBe("Bug fix");
    expect(event.parentConversationId).toBe("parent-conv");
    expect(event.parentTraceId).toBe("parent-trace");
  });

  it("creates a run_ended event with ok=true", () => {
    const event = createSubagentRunEndedEvent("run-3", "conv-3", "gemini", true, { summary: "Done" });
    expect(event.type).toBe("subagent.run_ended");
    expect(event.ok).toBe(true);
    expect(event.summary).toBe("Done");
    expect(event.error).toBeUndefined();
    expect(event.aggregateId).toBe("run-3");
  });

  it("creates a run_ended event with ok=false and error", () => {
    const event = createSubagentRunEndedEvent("run-4", "conv-4", "cursor", false, { error: "Provider unavailable" });
    expect(event.ok).toBe(false);
    expect(event.error).toBe("Provider unavailable");
    expect(event.summary).toBeUndefined();
  });

  it("events satisfy DomainEvent interface", () => {
    const started: SubagentRunStartedEvent = createSubagentRunStartedEvent("r", "c", "p", "prompt");
    const ended: SubagentRunEndedEvent = createSubagentRunEndedEvent("r", "c", "p", true);
    expect(typeof started.type).toBe("string");
    expect(started.occurredAt).toBeInstanceOf(Date);
    expect(typeof started.aggregateId).toBe("string");
    expect(typeof ended.type).toBe("string");
    expect(ended.occurredAt).toBeInstanceOf(Date);
    expect(typeof ended.aggregateId).toBe("string");
  });
});
