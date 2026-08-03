import { describe, expect, it } from "vitest";
import type { AgentMessage } from "../src/agent/ports/agent-provider.port.js";
import { ApplicationError } from "../src/errors/application-error.js";
import {
  clampText,
  formatMessagesForSummary,
  rethrowWithTurnPartial,
} from "../src/agent/services/agent-turn-utils.js";
import type { AgentTurnPartial } from "../src/agent/services/agent-turn-types.js";

const samplePartial: AgentTurnPartial = {
  traceId: "trace-1",
  rounds: 2,
  text: "",
  toolCalls: [],
  steps: [],
  messages: [{ role: "user", content: "hi" }],
};

describe("rethrowWithTurnPartial", () => {
  it("attaches partial to cancel so Stop can resume", () => {
    try {
      rethrowWithTurnPartial(
        new ApplicationError("AGENT_TURN_CANCELLED", "cancelled", { traceId: "t" }),
        samplePartial,
      );
      expect.unreachable("should throw");
    } catch (error) {
      expect(error).toMatchObject({
        code: "AGENT_TURN_CANCELLED",
        details: { traceId: "t", partial: samplePartial },
      });
    }
  });

  it("wraps allowlist errors with partial for resume", () => {
    try {
      rethrowWithTurnPartial(
        new ApplicationError("AGENT_TOOL_NOT_ALLOWED", "outside allowlist", { toolName: "x" }),
        samplePartial,
      );
      expect.unreachable("should throw");
    } catch (error) {
      expect(error).toMatchObject({
        code: "AGENT_TOOL_NOT_ALLOWED",
        details: { toolName: "x", partial: samplePartial, traceId: "trace-1" },
      });
    }
  });

  it("passes through errors that already carry partial", () => {
    const original = new ApplicationError("AGENT_PROVIDER_FAILED", "boom", {
      partial: samplePartial,
      cause: "429",
    });
    try {
      rethrowWithTurnPartial(original, { ...samplePartial, rounds: 9 });
      expect.unreachable("should throw");
    } catch (error) {
      expect(error).toBe(original);
    }
  });
});

describe("formatMessagesForSummary", () => {
  it("includes tool call args alongside names on assistant messages", () => {
    const messages: AgentMessage[] = [
      { role: "user", content: "write a file" },
      { role: "assistant", content: "Done.", toolCalls: [
        { id: "call-1", name: "files_write", args: { path: "/a.txt", content: "hi" } },
        { id: "call-2", name: "files_list", args: { path: "/" } },
      ] },
      { role: "tool", toolCallId: "call-1", name: "files_write", content: "wrote 2 bytes" },
      { role: "tool", toolCallId: "call-2", name: "files_list", content: "a.txt" },
    ];

    const summary = formatMessagesForSummary(messages);
    expect(summary).toContain("files_write(");
    expect(summary).toContain("/a.txt");
    expect(summary).toContain("files_list(");
    expect(summary).toContain("wrote 2 bytes");
  });

  it("appends assistant reasoning when present", () => {
    const messages: AgentMessage[] = [
      { role: "assistant", content: "Wrote it.", reasoning: "I chose /a.txt because the user asked for a scratch file.", toolCalls: [
        { id: "call-1", name: "files_write", args: { path: "/a.txt" } },
      ] },
      { role: "tool", toolCallId: "call-1", name: "files_write", content: "ok" },
    ];

    const summary = formatMessagesForSummary(messages);
    expect(summary).toContain("Reasoning:");
    expect(summary).toContain("scratch file");
  });

  it("scales the per-tool-result budget with summaryMaxChars and caps at 4000", () => {
    const longOutput = "x".repeat(10_000);
    const messages: AgentMessage[] = [
      { role: "tool", toolCallId: "c1", name: "files_read", content: longOutput },
    ];

    const small = formatMessagesForSummary(messages, 1_000);
    const large = formatMessagesForSummary(messages, 40_000);

    // summaryMaxChars=1000 → budget = max(800, 125) = 800
    expect(small).toContain(clampText(longOutput, 800));
    // summaryMaxChars=40000 → budget = min(4000, 5000) = 4000
    expect(large).toContain(clampText(longOutput, 4_000));
    expect(large.length).toBeGreaterThan(small.length);
  });

  it("keeps assistant → tool result join order so cause precedes effect", () => {
    const messages: AgentMessage[] = [
      { role: "assistant", content: "Writing.", toolCalls: [{ id: "c1", name: "files_write", args: { path: "/a" } }] },
      { role: "tool", toolCallId: "c1", name: "files_write", content: "wrote" },
    ];

    const summary = formatMessagesForSummary(messages);
    const assistantIdx = summary.indexOf("Assistant:");
    const toolIdx = summary.indexOf("Tool files_write:");
    expect(assistantIdx).toBeGreaterThanOrEqual(0);
    expect(toolIdx).toBeGreaterThan(assistantIdx);
  });

  it("truncates tool args to ~400 chars in the summary", () => {
    const bigArgs = { content: "y".repeat(5_000) };
    const messages: AgentMessage[] = [
      { role: "assistant", content: "", toolCalls: [{ id: "c1", name: "files_write", args: bigArgs }] },
    ];

    const summary = formatMessagesForSummary(messages);
    // The args JSON is clamped to 400 chars; the full 5000-char content must not survive.
    expect(summary).not.toContain("y".repeat(500));
    expect(summary.length).toBeLessThan(1_000);
  });
});
