// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { AgentConversationController } from "../src/renderer/agent-conversation-controller.js";

function installDom() {
  document.body.innerHTML = `
    <aside id="agent-subpane" hidden>
      <div id="agent-subpane-overlay" hidden></div>
      <span id="agent-subpane-badge"></span>
      <span id="agent-subpane-title"></span>
      <span id="agent-subpane-status"></span>
      <button id="agent-subpane-close"></button>
      <div id="agent-subpane-body"></div>
    </aside>
  `;
  globalThis.$ = (selector: string) => document.querySelector(selector);
  window.matchMedia = vi.fn(() => ({ matches: true })) as typeof window.matchMedia;
}

describe("AgentConversationController — subagent stream pane", () => {
  beforeEach(() => installDom());

  it("preserves live text when a running pane is closed and remounted", () => {
    const controller = new AgentConversationController({} as never);
    const run = {
      runId: "trace-1",
      providerId: "cursor",
      prompt: "Fix it",
      status: "running",
      steps: [],
    };

    controller.activeSubagentRun = run;
    controller.resetSubagentStreamState([], run.runId);
    controller.appendSubpaneText("Live output");
    controller.closeSubpaneDrawerUi();
    controller.mountSubpane(run, { open: true });

    expect(document.querySelector("#agent-subpane-body")?.textContent).toContain("Live output");
    expect(controller.subagentStreamState?.textContent).toBe("Live output");
  });

  it("renders sealed stream steps when the run ends", async () => {
    const updateSubagentRunStatus = vi.fn().mockResolvedValue({ id: "conv-1" });
    const controller = new AgentConversationController({
      shell: { agentConversations: { updateSubagentRunStatus } },
    } as never);
    controller.conversation = { id: "conv-1", messages: [] } as never;
    controller.activeSubagentRun = {
      runId: "trace-2",
      providerId: "cursor",
      prompt: "Fix it",
      status: "running",
      steps: [],
    } as never;
    controller.resetSubagentStreamState([], "trace-2");
    controller.appendSubpaneText("Completed output");

    controller.handleSubagentRunEnded({ runId: "trace-2", ok: true, summary: "Done" });
    await Promise.resolve();

    expect(document.querySelector("#agent-subpane-body")?.textContent).toContain("Completed output");
    expect(updateSubagentRunStatus).toHaveBeenCalledWith(
      "conv-1",
      "trace-2",
      "ok",
      expect.objectContaining({ summary: "Done", steps: [{ type: "text", content: "Completed output" }] }),
    );
  });
});

describe("AgentConversationController — in-chat subagent mini stream", () => {
  beforeEach(() => installDom());

  it("mounts a mini stream viewport on a running subagent card", () => {
    const controller = new AgentConversationController({} as never);
    const card = controller.renderSubagentCard({
      runId: "run-a",
      providerId: "cursor",
      title: "Refactor",
      status: "running",
    });

    expect(card.querySelector(".agent-subagent-card-stream")).not.toBeNull();
    expect(card.dataset.runId).toBe("run-a");
  });

  it("omits the mini stream on sealed (non-running) cards", () => {
    const controller = new AgentConversationController({} as never);
    const card = controller.renderSubagentCard({
      runId: "run-b",
      providerId: "cursor",
      title: "Done",
      status: "ok",
      summary: "Shipped",
    });

    expect(card.querySelector(".agent-subagent-card-stream")).toBeNull();
    expect(card.querySelector(".agent-subagent-card-summary")).not.toBeNull();
  });

  it("attaches the mini stream to the in-chat card on run start and appends rows", () => {
    const upsertSubagentRun = vi.fn().mockResolvedValue({ id: "conv-1", messages: [] });
    const setActiveSubagentRun = vi.fn().mockResolvedValue({ id: "conv-1", messages: [] });
    const controller = new AgentConversationController({
      shell: { agentConversations: { upsertSubagentRun, setActiveSubagentRun } },
    } as never);
    controller.conversation = { id: "conv-1", messages: [] } as never;
    // Simulate the parent turn creating a streaming subagent card.
    const card = controller.createStreamingToolCard("call-1", "subagent", { prompt: "Fix it", title: "Refactor", provider_id: "cursor" });
    document.body.appendChild(card);

    // subagent.run_started bridges the card to the real runId.
    controller.handleSubagentRunStarted({
      runId: "run-c",
      providerId: "cursor",
      title: "Refactor",
      prompt: "Fix it",
    });

    expect(card.dataset.runId).toBe("run-c");
    expect(controller.activeSubagentCardStream?.runId).toBe("run-c");

    controller.appendCardStreamText("Working on it");
    controller.appendCardStreamToolCall({ id: "tc-1", title: "edit_file", status: "running", args: { path: "src/a.ts" } });
    controller.updateCardStreamToolCall("tc-1", "ok", "edited");

    const rows = card.querySelectorAll(".agent-subagent-card-stream-row");
    expect(rows.length).toBeGreaterThanOrEqual(2);
    expect(card.querySelector(".agent-subagent-card-stream-row.is-text")?.textContent).toContain("Working on it");
    expect(card.querySelector(".agent-subagent-card-stream-row.is-tool.is-ok")?.textContent).toContain("edit_file");
  });

  it("auto-scrolls the mini stream to the bottom while pinned", () => {
    const controller = new AgentConversationController({} as never);
    const card = controller.renderSubagentCard({ runId: "run-d", providerId: "cursor", title: "T", status: "running" });
    document.body.appendChild(card);
    controller.attachSubagentCardStream("run-d");
    const stream = card.querySelector(".agent-subagent-card-stream") as HTMLElement;
    // jsdom does not layout, so force a scrollable size.
    Object.defineProperty(stream, "scrollHeight", { value: 500, configurable: true });
    Object.defineProperty(stream, "clientHeight", { value: 100, configurable: true });

    controller.appendCardStreamText("line one");
    controller.appendCardStreamText("line two");

    expect(stream.scrollTop).toBe(500);
  });

  it("pauses stickiness when the user scrolls up", () => {
    const controller = new AgentConversationController({} as never);
    const card = controller.renderSubagentCard({ runId: "run-e", providerId: "cursor", title: "T", status: "running" });
    document.body.appendChild(card);
    controller.attachSubagentCardStream("run-e");
    const stream = card.querySelector(".agent-subagent-card-stream") as HTMLElement;
    Object.defineProperty(stream, "scrollHeight", { value: 1000, configurable: true });
    Object.defineProperty(stream, "clientHeight", { value: 100, configurable: true });

    controller.appendCardStreamText("first");
    expect(stream.scrollTop).toBe(1000);

    // User scrolls up away from the bottom.
    stream.scrollTop = 200;
    stream.dispatchEvent(new Event("scroll"));
    expect(controller.activeSubagentCardStream?.pinned).toBe(false);

    // Appending more text should NOT auto-scroll while unpinned.
    controller.appendCardStreamText("second");
    expect(stream.scrollTop).toBe(200);

    // User returns to the bottom; stickiness resumes.
    stream.scrollTop = 1000;
    stream.dispatchEvent(new Event("scroll"));
    expect(controller.activeSubagentCardStream?.pinned).toBe(true);
    controller.appendCardStreamText("third");
    expect(stream.scrollTop).toBe(1000);
  });

  it("disposes the mini stream state on run end without removing the frozen tail", () => {
    const controller = new AgentConversationController({} as never);
    const card = controller.renderSubagentCard({ runId: "run-f", providerId: "cursor", title: "T", status: "running" });
    document.body.appendChild(card);
    controller.attachSubagentCardStream("run-f");
    controller.appendCardStreamText("frozen tail");

    controller.handleSubagentRunEnded({ runId: "run-f", ok: true, summary: "Done" });

    expect(controller.activeSubagentCardStream).toBeNull();
    // The frozen rows remain in the DOM until the parent turn replaces the card.
    expect(card.querySelector(".agent-subagent-card-stream-row")?.textContent).toContain("frozen tail");
  });

  it("prunes the mini stream to the last 50 rows", () => {
    const controller = new AgentConversationController({} as never);
    const card = controller.renderSubagentCard({ runId: "run-g", providerId: "cursor", title: "T", status: "running" });
    document.body.appendChild(card);
    controller.attachSubagentCardStream("run-g");

    // Each tool call with a distinct id creates a new row (text deltas coalesce).
    for (let i = 0; i < 60; i += 1) {
      controller.appendCardStreamToolCall({ id: `tc-${i}`, title: "edit_file", status: "running", args: { path: `src/${i}.ts` } });
    }

    const rows = card.querySelectorAll(".agent-subagent-card-stream-row");
    expect(rows.length).toBe(50);
  });
});
