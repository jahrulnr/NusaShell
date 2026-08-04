// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";

import { CompletionSteerer } from "../src/renderer/completion-steerer.js";

describe("CompletionSteerer", () => {
  let startedTurns: string[];
  let idleState: boolean;
  let logs: string[];

  beforeEach(() => {
    startedTurns = [];
    idleState = true;
    logs = [];
  });

  function makeSteerer() {
    return new CompletionSteerer({
      conversationId: "conv-1",
      isIdle: () => idleState,
      startTurn: async (message) => { startedTurns.push(message); },
      log: (msg) => logs.push(msg),
    });
  }

  it("does nothing when no jobs end", () => {
    const steerer = makeSteerer();
    steerer.dispose();
    expect(startedTurns).toHaveLength(0);
  });

  it("auto-starts a turn when a job ends and conversation is idle", async () => {
    const steerer = makeSteerer();
    steerer.onJobEnded({ handleId: "h1", conversationId: "conv-1", ok: true, reason: "completed", toolName: "run_command" });
    // Wait for debounce + fire.
    await new Promise((r) => setTimeout(r, 600));
    expect(startedTurns).toHaveLength(1);
    expect(startedTurns[0]).toContain("Background job completed");
    expect(startedTurns[0]).toContain("run_command");
    steerer.dispose();
  });

  it("coalesces multiple job completions into one turn", async () => {
    const steerer = makeSteerer();
    steerer.onJobEnded({ handleId: "h1", conversationId: "conv-1", ok: true, reason: "completed", toolName: "tool_a" });
    steerer.onJobEnded({ handleId: "h2", conversationId: "conv-1", ok: false, reason: "failed", toolName: "tool_b", error: "boom" });
    await new Promise((r) => setTimeout(r, 600));
    expect(startedTurns).toHaveLength(1);
    expect(startedTurns[0]).toContain("tool_a");
    expect(startedTurns[0]).toContain("tool_b");
    steerer.dispose();
  });

  it("does not start a turn when conversation has an active turn", async () => {
    idleState = false;
    const steerer = makeSteerer();
    steerer.onJobEnded({ handleId: "h1", conversationId: "conv-1", ok: true, reason: "completed", toolName: "t" });
    await new Promise((r) => setTimeout(r, 600));
    expect(startedTurns).toHaveLength(0);
    expect(logs.some((l) => l.includes("skipped"))).toBe(true);
    steerer.dispose();
  });

  it("ignores events for other conversations", async () => {
    const steerer = makeSteerer();
    steerer.onJobEnded({ handleId: "h1", conversationId: "conv-other", ok: true, reason: "completed", toolName: "t" });
    await new Promise((r) => setTimeout(r, 600));
    expect(startedTurns).toHaveLength(0);
    steerer.dispose();
  });

  it("dispose cancels pending wake", async () => {
    const steerer = makeSteerer();
    steerer.onJobEnded({ handleId: "h1", conversationId: "conv-1", ok: true, reason: "completed", toolName: "t" });
    steerer.dispose();
    await new Promise((r) => setTimeout(r, 600));
    expect(startedTurns).toHaveLength(0);
  });

  it("includes error and output in the summary", async () => {
    const steerer = makeSteerer();
    steerer.onJobEnded({
      handleId: "h1",
      conversationId: "conv-1",
      ok: false,
      reason: "failed",
      toolName: "build",
      error: "exit code 1",
      output: { stdout: "error: syntax error" },
    });
    await new Promise((r) => setTimeout(r, 600));
    expect(startedTurns[0]).toContain("exit code 1");
    expect(startedTurns[0]).toContain("stdout");
    steerer.dispose();
  });
});
