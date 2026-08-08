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
        { id: "call-2", name: "read", ok: true, args: { path: "/a" }, output: "hi" },
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
    expect(message.steps?.map((step) => step.stepPosition)).toEqual([1, 2]);
    const toolStep = message.steps?.find((step) => step.type === "tool_calls");
    expect(toolStep?.type === "tool_calls" ? toolStep.calls.map((call) => call.callPosition) : []).toEqual([1]);
  });

  it("defaults missing args to {} in buildToolCall", () => {
    const call = buildToolCall({ id: "c1", name: "mcp_list", ok: true, output: "[]" });
    expect(call.args).toEqual({});
    expect(call.id).toBe("c1");
    expect(call.name).toBe("mcp_list");
    expect(call.ok).toBe(true);
  });

  it("persists the exact provider-facing tool projection for the UI", () => {
    const modelOutput = '<untrusted_tool_result source="mcp_files_list" format="terminal">\n' +
      "status=success\ntruncated=false\n\nentries[1]\npath\tkind\ndocs/a.md\tfile\n" +
      "</untrusted_tool_result>";
    const result: AgentTurnResult = {
      traceId: "trace-terminal",
      text: "Done.",
      rounds: 1,
      toolCalls: [{ id: "c-terminal", name: "mcp_files_list", ok: true, args: {}, result: { ignored: true }, modelOutput }],
    };

    const message = buildAssistantMessage(result);
    const call = message.toolCalls?.[0];
    expect(call?.modelOutput).toContain("status=success");
    expect(call?.output).toBe(call?.modelOutput);
    expect(call?.output).toContain("<untrusted_tool_result source=\"mcp_files_list\" format=\"terminal\">");
  });

  it("preserves typed structured content from the canonical tool result", () => {
    const structuredContent = {
      ok: true,
      runId: "run-structured",
      providerId: "cursor",
      summary: "Complete response",
    };
    const call = buildToolCall({
      id: "subagent:0",
      name: "subagent",
      ok: true,
      modelOutput: "status=success\ntruncated=false\n\nrunId=run-structured",
      toolResult: {
        callId: "subagent:0",
        toolName: "subagent",
        status: "success",
        content: [{ type: "json", data: structuredContent }],
        structuredContent,
        metadata: { truncated: false, dataIsUntrusted: false },
      },
    });

    expect(call.structuredContent).toEqual(structuredContent);
    expect(call.status).toBe("success");
    expect(call.truncated).toBe(false);
  });

  it("builds an interrupted message from a partial with live text and resumeMessages", () => {
    const resume = [
      { role: "user" as const, content: "go" },
      { role: "assistant" as const, toolCalls: [{ id: "call-1", name: "read", args: { path: "/a" } }] },
      { role: "tool" as const, toolCallId: "call-1", name: "read", content: "hi" },
    ];
    const partial: AgentTurnPartial = {
      traceId: "trace-2",
      rounds: 2,
      text: "Halfway through the essay",
      toolCalls: [{ id: "call-1", name: "read", ok: true, args: { path: "/a" }, output: "hi" }],
      steps: [{ type: "text", content: "Halfway through the essay" }],
      messages: resume as AgentTurnPartial["messages"],
    };
    const message = buildInterruptedMessage(partial, { interruptReason: "provider" });
    expect(message.role).toBe("assistant");
    expect(message.status).toBe("interrupted");
    expect(message.interruptReason).toBe("provider");
    expect(message.traceId).toBe("trace-2");
    expect(message.rounds).toBe(2);
    // Prefer live body over stub so Retry can text-Continue.
    expect(message.content).toBe("Halfway through the essay");
    expect(message.toolCalls).toHaveLength(1);
    expect(message.toolCalls?.[0].args).toEqual({ path: "/a" });
    expect(message.resumeMessages).toEqual(resume);
  });

  it("falls back to interrupted stub when partial has no live text", () => {
    const partial: AgentTurnPartial = {
      traceId: "trace-2b",
      rounds: 3,
      text: "",
      toolCalls: [{ id: "call-1", name: "read", ok: true, args: {}, output: "ok" }],
      steps: [],
      // Tool result must appear in the graph so resume is eligible (not inject-only).
      messages: [
        { role: "user", content: "go" },
        { role: "assistant", toolCalls: [{ id: "call-1", name: "read", args: {} }] },
        { role: "tool", toolCallId: "call-1", name: "read", content: "ok" },
      ] as AgentTurnPartial["messages"],
    };
    const message = buildInterruptedMessage(partial, { interruptReason: "cancel" });
    expect(message.content).toContain("interrupted after 3 tool rounds");
    expect(message.interruptReason).toBe("cancel");
    expect(message.resumeMessages).toHaveLength(3);
  });

  it("omits resumeMessages on pure-text / pre-tool partial (inject-only messages)", () => {
    const partial: AgentTurnPartial = {
      traceId: "trace-2c",
      rounds: 0,
      text: "Siap! Baca skill dulu.",
      reasoning: "Matches codebase-review skill.",
      toolCalls: [],
      steps: [],
      messages: [
        { role: "system", content: "You are the NusaShell agent." },
        { role: "user", content: "Hunt bugs" },
      ],
    };
    const message = buildInterruptedMessage(partial, { interruptReason: "provider" });
    expect(message.content).toBe("Siap! Baca skill dulu.");
    expect(message.reasoning).toBe("Matches codebase-review skill.");
    expect(message.resumeMessages).toBeUndefined();
    expect(message.toolCalls).toBeUndefined();
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
    const call = buildToolCall({ id: "c1", name: "write", ok: true, args: { path: "/a.txt", content: huge } });
    expect(JSON.stringify(call.args).length).toBeLessThanOrEqual(8_000);
  });
});
