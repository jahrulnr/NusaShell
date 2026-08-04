import { describe, expect, it, vi } from "vitest";
import { SubagentRunLifecycle } from "../src/renderer/subagent-run-lifecycle.js";

describe("SubagentRunLifecycle", () => {
  it("starts in idle state with all fields cleared", () => {
    const lc = new SubagentRunLifecycle();
    expect(lc.activeRun).toBeNull();
    expect(lc.streamState).toBeNull();
    expect(lc.streamDisposer).toBeNull();
    expect(lc.cardStream).toBeNull();
    expect(lc.eventDisposer).toBeNull();
    expect(lc.ownerConversationId).toBeNull();
  });

  it("reset() clears all fields", () => {
    const lc = new SubagentRunLifecycle();
    lc.activeRun = { runId: "r1" };
    lc.streamState = { runId: "r1", steps: [] };
    lc.cardStream = { runId: "r1" };
    lc.ownerConversationId = "conv-1";
    lc.reset();
    expect(lc.activeRun).toBeNull();
    expect(lc.streamState).toBeNull();
    expect(lc.cardStream).toBeNull();
    expect(lc.ownerConversationId).toBeNull();
  });

  it("rebindEvents disposes previous disposer and binds new one", () => {
    const lc = new SubagentRunLifecycle();
    const oldDispose = vi.fn();
    const newDispose = vi.fn();
    lc.rebindEvents(() => oldDispose);
    lc.rebindEvents(() => newDispose);
    expect(oldDispose).toHaveBeenCalledTimes(1);
    expect(lc.eventDisposer).toBe(newDispose);
  });

  it("rebindEvents logs info on success and error on null disposer", () => {
    const log = vi.fn();
    const lc = new SubagentRunLifecycle(log);
    lc._conversationId = "conv-1";
    lc.rebindEvents(() => () => {});
    expect(log).toHaveBeenCalledTimes(1);
    expect(log.mock.calls[0][0]).toBe("info");
    expect(log.mock.calls[0][1]).toContain("conv-1");

    lc.rebindEvents(() => null);
    expect(log).toHaveBeenCalledTimes(2);
    expect(log.mock.calls[1][0]).toBe("error");
    expect(log.mock.calls[1][1]).toContain("subagentEventDisposer is null");
  });

  it("startRun disposes old stream and binds new one", () => {
    const lc = new SubagentRunLifecycle();
    const oldDispose = vi.fn();
    const newDispose = vi.fn();
    lc.startRun(() => oldDispose);
    lc.startRun(() => newDispose);
    expect(oldDispose).toHaveBeenCalledTimes(1);
    expect(lc.streamDisposer).toBe(newDispose);
  });

  it("endRunDisposeStream disposes stream but keeps state for snapshotting", () => {
    const lc = new SubagentRunLifecycle();
    const dispose = vi.fn();
    lc.startRun(() => dispose);
    lc.streamState = { runId: "r1", steps: [{ type: "text", content: "x" }] };
    lc.ownerConversationId = "conv-1";
    lc.endRunDisposeStream();
    expect(dispose).toHaveBeenCalledTimes(1);
    expect(lc.streamDisposer).toBeNull();
    // State preserved for snapshotSubagentSteps
    expect(lc.streamState).not.toBeNull();
    expect(lc.ownerConversationId).toBe("conv-1");
  });

  it("endRunClearState clears stream state and owner after snapshot", () => {
    const lc = new SubagentRunLifecycle();
    lc.streamState = { runId: "r1", steps: [] };
    lc.ownerConversationId = "conv-1";
    lc.endRunClearState();
    expect(lc.streamState).toBeNull();
    expect(lc.ownerConversationId).toBeNull();
  });

  it("dispose() tears down everything", () => {
    const lc = new SubagentRunLifecycle();
    const eventDispose = vi.fn();
    const streamDispose = vi.fn();
    lc.rebindEvents(() => eventDispose);
    lc.startRun(() => streamDispose);
    lc.activeRun = { runId: "r1" };
    lc.streamState = { runId: "r1", steps: [] };
    lc.cardStream = { runId: "r1" };
    lc.ownerConversationId = "conv-1";
    lc.dispose();
    expect(eventDispose).toHaveBeenCalledTimes(1);
    expect(streamDispose).toHaveBeenCalledTimes(1);
    expect(lc.eventDisposer).toBeNull();
    expect(lc.streamDisposer).toBeNull();
    expect(lc.activeRun).toBeNull();
    expect(lc.streamState).toBeNull();
    expect(lc.cardStream).toBeNull();
    expect(lc.ownerConversationId).toBeNull();
  });

  it("isViewingOwner returns true when no owner tracked", () => {
    const lc = new SubagentRunLifecycle();
    expect(lc.isViewingOwner("conv-1")).toBe(true);
    expect(lc.isViewingOwner(undefined)).toBe(true);
  });

  it("isViewingOwner matches owner conversation id", () => {
    const lc = new SubagentRunLifecycle();
    lc.ownerConversationId = "conv-1";
    expect(lc.isViewingOwner("conv-1")).toBe(true);
    expect(lc.isViewingOwner("conv-2")).toBe(false);
  });
});
