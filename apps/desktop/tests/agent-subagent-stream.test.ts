// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { AgentConversationController } from "../src/renderer/agent-conversation-controller.js";

function installDom() {
  document.body.innerHTML = `
    <input id="agent-input">
    <button id="agent-send-btn"></button>
    <button id="agent-stop-btn"></button>
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

describe("AgentConversationController — conversation-scoped composer state", () => {
  beforeEach(() => installDom());

  it("allows New conversation while another turn is running", async () => {
    const createConversation = vi.fn().mockResolvedValue({ id: "conversation-b", messages: [] });
    const controller = new AgentConversationController({
      shell: { agentConversations: { create: createConversation } },
    } as never);
    controller.conversation = { id: "conversation-a", messages: [{ role: "user", content: "Working" }] } as never;
    controller.turnPending = true;
    controller.resetComposerForConversation = vi.fn();
    controller.renderThread = vi.fn();
    controller.updateWorkspaceLabel = vi.fn();
    controller.updateContextStatus = vi.fn();
    controller.updateAcpStatus = vi.fn();
    controller.refresh = vi.fn().mockResolvedValue(undefined);

    await controller.create(undefined, { bypassTurnGuard: true });

    expect(createConversation).toHaveBeenCalledOnce();
    expect(controller.activeId).toBe("conversation-b");
  });

  it("resets composer controls when switching away from a running conversation", () => {
    const controller = new AgentConversationController({} as never);
    const input = document.querySelector<HTMLInputElement>("#agent-input")!;
    const send = document.querySelector<HTMLButtonElement>("#agent-send-btn")!;
    const stop = document.querySelector<HTMLButtonElement>("#agent-stop-btn")!;

    controller.turnPending = true;
    controller.turnOwnerConversationId = "conversation-a";
    input.disabled = true;
    send.disabled = true;
    stop.hidden = false;
    stop.classList.add("is-stopping");

    controller.resetComposerForConversation("conversation-b");

    expect(input.disabled).toBe(false);
    expect(send.disabled).toBe(false);
    expect(stop.hidden).toBe(true);
    expect(stop.disabled).toBe(false);
    expect(stop.classList.contains("is-stopping")).toBe(false);
  });

  it("keeps the stop state when the active conversation owns the turn", () => {
    const controller = new AgentConversationController({} as never);
    const input = document.querySelector<HTMLInputElement>("#agent-input")!;
    const send = document.querySelector<HTMLButtonElement>("#agent-send-btn")!;
    const stop = document.querySelector<HTMLButtonElement>("#agent-stop-btn")!;

    controller.turnPending = true;
    controller.turnOwnerConversationId = "conversation-a";
    controller.resetComposerForConversation("conversation-a");

    expect(input.disabled).toBe(true);
    expect(send.disabled).toBe(true);
    expect(stop.hidden).toBe(false);
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

  it("renders markdown HTML in thinking/text card stream rows", () => {
    const controller = new AgentConversationController({} as never);
    const card = controller.renderSubagentCard({ runId: "run-md", providerId: "gemini", title: "T", status: "running" });
    document.body.appendChild(card);
    controller.attachSubagentCardStream("run-md");
    controller.appendCardStreamText("## Profile\n\n**Bold** line");

    const textEl = card.querySelector(".agent-subagent-card-stream-row.is-text .agent-subagent-card-stream-text");
    expect(textEl?.tagName).toBe("DIV");
    expect(textEl?.querySelector("strong")?.textContent).toBe("Bold");
    expect(textEl?.querySelector("h2")?.textContent).toBe("Profile");
    // No raw markdown fences left for closed tokens.
    expect(textEl?.textContent).not.toContain("**");
    expect(textEl?.textContent).not.toContain("##");
  });

  it("strips markdown from compact tool summary lines", () => {
    const controller = new AgentConversationController({} as never);
    const card = controller.renderSubagentCard({ runId: "run-md2", providerId: "gemini", title: "T", status: "running" });
    document.body.appendChild(card);
    controller.attachSubagentCardStream("run-md2");
    controller.appendCardStreamToolCall({ id: "tc-md", title: "Update topic", status: "running", args: {} });
    controller.updateCardStreamToolCall("tc-md", "ok", '## 📁 Topic: **Create Personal Profile Page**');

    const text = card.querySelector(".agent-subagent-card-stream-row.is-tool .agent-subagent-card-stream-text")?.textContent ?? "";
    expect(text).toContain("Topic:");
    expect(text).toContain("Create Personal Profile Page");
    expect(text).not.toContain("##");
    expect(text).not.toContain("**");
  });

  it("does not reset the live stream when the card still closes over the tool callId", () => {
    const controller = new AgentConversationController({
      shell: {
        agentConversations: {
          upsertSubagentRun: vi.fn().mockResolvedValue({ id: "conv-live", messages: [], subagentRuns: [] }),
          setActiveSubagentRun: vi.fn().mockResolvedValue({ id: "conv-live", messages: [], subagentRuns: [] }),
        },
      },
    } as never);
    controller.conversation = { id: "conv-live", messages: [] } as never;

    // Parent tool card is keyed by tool call id until run_started rewrites dataset.runId.
    const card = controller.createStreamingToolCard("call-tool-1", "subagent", {
      prompt: "Fix it",
      title: "Refactor",
      provider_id: "gemini",
    });
    document.body.appendChild(card);
    controller.handleSubagentRunStarted({
      runId: "acp-run-1",
      providerId: "gemini",
      title: "Refactor",
      prompt: "Fix it",
    });
    controller.appendSubpaneText("Live sidebar body");
    expect(document.querySelector("#agent-subpane-body")?.textContent).toContain("Live sidebar body");
    expect(controller.subagentStreamState?.runId).toBe("acp-run-1");

    // Click uses dataset.runId (real) after attach; even if callId was used, mount must not wipe.
    card.querySelector(".agent-subagent-card-head")?.dispatchEvent(new Event("click"));

    expect(controller.subagentStreamState?.runId).toBe("acp-run-1");
    expect(document.querySelector("#agent-subpane-body")?.textContent).toContain("Live sidebar body");
  });

  it("recreates a running subagent card after renderThread (chat switch)", () => {
    document.body.innerHTML += `<div id="agent-thread"></div>`;
    const controller = new AgentConversationController({} as never);
    controller.conversation = {
      id: "conv-switch",
      kind: "agent",
      messages: [{ role: "user", content: "make a profile", id: "m1" }],
      subagentRuns: [{
        runId: "run-switch",
        providerId: "gemini",
        title: "Web Profile HTML",
        status: "running",
        steps: [],
      }],
      activeSubagentRunId: "run-switch",
    } as never;
    controller.subagentOwnerConversationId = "conv-switch";
    controller.resetSubagentStreamState([], "run-switch");
    controller.subagentStreamState!.textContent = "Rebuilt on return";
    controller.subagentStreamState!.lastKind = "text";

    controller.renderThread();

    const card = document.querySelector(".agent-subagent-card[data-run-id=\"run-switch\"]");
    expect(card).not.toBeNull();
    expect(card?.querySelector(".agent-subagent-card-status")?.textContent).toMatch(/RUNNING/i);
    // Rebuilt mini stream should show the live text snapshot.
    expect(card?.querySelector(".agent-subagent-card-stream-row.is-text")?.textContent).toContain("Rebuilt on return");
  });
});
