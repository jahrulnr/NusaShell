import { describe, expect, it } from "vitest";
import { buildAssistantMessage, buildInterruptedMessage, buildToolCall } from "../src/shared/agent-message-builder.js";
import type { AgentTurnResult, AgentTurnPartial } from "@nusashell/application";

describe("agent-message-builder", () => {
  it("builds an assistant message with args defaulted to {}", () => {
    const result: AgentTurnResult = {
      traceId: "trace-1",
      text: "Done.",
      rounds: 1,
      toolCalls: [
        { id: "call-1", name: "mcp_list", ok: true, args: {}, output: "[]" },
        { id: "call-2", name: "files_read", ok: true, args: { path: "/a" }, output: "hi" },
      ],
      steps: [
        { type: "text", content: "Done." },
        { type: "tool_calls", calls: [{ id: "call-1", name: "mcp_list", ok: true, args: {}, output: "[]" }] },
      ],
      model: "gpt-4",
    };
    const message = buildAssistantMessage(result);
    expect(message.role).toBe("assistant");
    expect(message.content).toBe("Done.");
    expect(message.traceId).toBe("trace-1");
    expect(message.model).toBe("gpt-4");
    expect(message.rounds).toBe(1);
    expect(message.toolCalls).toHaveLength(2);
    expect(message.toolCalls?.[0].args).toEqual({});
    expect(message.toolCalls?.[1].args).toEqual({ path: "/a" });
    expect(message.steps).toHaveLength(2);
  });

  it("defaults missing args to {} in buildToolCall", () => {
    const call = buildToolCall({ id: "c1", name: "mcp_list", ok: true, output: "[]" });
    expect(call.args).toEqual({});
    expect(call.id).toBe("c1");
    expect(call.name).toBe("mcp_list");
    expect(call.ok).toBe(true);
  });

  it("builds an interrupted message from a partial", () => {
    const partial: AgentTurnPartial = {
      traceId: "trace-2",
      rounds: 2,
      text: "partial",
      toolCalls: [{ id: "call-1", name: "files_read", ok: true, args: { path: "/a" }, output: "hi" }],
      steps: [{ type: "text", content: "partial" }],
      messages: [],
    };
    const message = buildInterruptedMessage(partial);
    expect(message.role).toBe("assistant");
    expect(message.status).toBe("interrupted");
    expect(message.traceId).toBe("trace-2");
    expect(message.rounds).toBe(2);
    expect(message.content).toContain("interrupted after 2 tool rounds");
    expect(message.toolCalls).toHaveLength(1);
    expect(message.toolCalls?.[0].args).toEqual({ path: "/a" });
  });

  it("omits toolCalls when empty", () => {
    const result: AgentTurnResult = {
      traceId: "trace-3",
      text: "Hello.",
      rounds: 0,
      toolCalls: [],
      steps: [{ type: "text", content: "Hello." }],
    };
    const message = buildAssistantMessage(result);
    expect(message.toolCalls).toBeUndefined();
  });

  it("clamps huge args within the cap", () => {
    const huge = "z".repeat(20_000);
    const call = buildToolCall({ id: "c1", name: "files_write", ok: true, args: { path: "/a.txt", content: huge } });
    expect(JSON.stringify(call.args).length).toBeLessThanOrEqual(8_000);
  });
});
