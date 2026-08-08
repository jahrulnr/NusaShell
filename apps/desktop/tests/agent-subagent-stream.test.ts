// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { AgentConversationController } from "../src/renderer/agent-conversation-controller.js";

function installDom() {
  document.body.innerHTML = `
    <input id="agent-input">
    <button id="agent-send-btn"></button>
    <button id="agent-stop-btn"></button>
    <section id="agent-attention-stack" hidden>
      <div class="agent-attention-title"></div>
      <div class="agent-attention-copy"></div>
      <span id="agent-attention-count"></span>
      <div id="agent-attention-list"></div>
    </section>
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
      expect.objectContaining({ summary: "Done", steps: [{ type: "text", stepPosition: 1, content: "Completed output" }] }),
    );
  });

  it("renders a terminal summary in the drawer when no stream steps were persisted", () => {
    const controller = new AgentConversationController({} as never);

    controller.mountSubpane({
      runId: "trace-summary-only",
      providerId: "cursor",
      prompt: "Say hello",
      status: "ok",
      summary: "Hello! Subagent reporting for duty.",
    }, { open: true });

    expect(document.querySelector("#agent-subpane-body")?.textContent)
      .toContain("Hello! Subagent reporting for duty.");
  });

  it("failStrandedSubagentRuns marks running runs fail and clears active", async () => {
    const updateSubagentRunStatus = vi.fn().mockImplementation(async (_id, runId, status, patch) => ({
      id: "conv-1",
      activeSubagentRunId: status === "fail" ? undefined : "run-live",
      subagentRuns: [{
        runId,
        status,
        error: patch?.error,
      }],
    }));
    const setActiveSubagentRun = vi.fn().mockResolvedValue({
      id: "conv-1",
      subagentRuns: [{ runId: "run-live", status: "fail", error: "Subagent run did not finish before the parent turn ended." }],
    });
    const controller = new AgentConversationController({
      shell: { agentConversations: { updateSubagentRunStatus, setActiveSubagentRun } },
    } as never);
    controller.conversation = {
      id: "conv-1",
      messages: [],
      activeSubagentRunId: "run-live",
      subagentRuns: [{
        id: "s1",
        conversationId: "conv-1",
        sourceMessageId: "1",
        runId: "run-live",
        providerId: "devin",
        title: "Deep-dive",
        prompt: "x",
        status: "running",
        createdAt: "t",
        updatedAt: "t",
      }],
    } as never;
    controller.turnOwnerConversationId = "conv-1";

    await controller.failStrandedSubagentRuns("Subagent run did not finish before the parent turn ended.");

    expect(updateSubagentRunStatus).toHaveBeenCalledWith(
      "conv-1",
      "run-live",
      "fail",
      { error: "Subagent run did not finish before the parent turn ended." },
    );
    expect(setActiveSubagentRun).toHaveBeenCalledWith("conv-1", null);
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
    controller.pendingTurnConversations.add("conversation-a");
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

    controller.turnOwnerConversationId = "conversation-a";
    controller.pendingTurnConversations.add("conversation-a");
    input.disabled = true;
    send.disabled = true;
    stop.hidden = false;
    stop.classList.add("is-stopping");
    input.value = "draft";

    controller.resetComposerForConversation("conversation-b");

    expect(input.disabled).toBe(false);
    // With content in the input the send button re-enables (#46).
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

    controller.turnOwnerConversationId = "conversation-a";
    controller.pendingTurnConversations.add("conversation-a");
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

  it("shows the dispatched prompt in a compact task strip", () => {
    const controller = new AgentConversationController({} as never);
    const prompt = "Inspect the conversation list and report why previews are missing.";
    const card = controller.renderSubagentCard({
      runId: "run-prompt",
      providerId: "cursor",
      title: "Audit conversations",
      prompt,
      status: "running",
    });

    const task = card.querySelector(".agent-subagent-card-prompt");
    expect(task?.textContent).toContain("TASK");
    expect(task?.textContent).toContain(prompt);
    expect(task?.querySelector(".agent-subagent-card-prompt-text")?.getAttribute("title")).toBe(prompt);
  });

  it("keeps ordinary tool cards collapsed while the tool is running", () => {
    const controller = new AgentConversationController({} as never);
    const card = controller.createStreamingToolCard("call-tool", "shell", { command: "pwd" });

    expect(card.tagName).toBe("DETAILS");
    expect((card as HTMLDetailsElement).open).toBe(false);
  });

  it("requests a follow scroll when a tool card settles", () => {
    const controller = new AgentConversationController({} as never);
    const card = controller.createStreamingToolCard("call-tool", "shell", { command: "pwd" });
    const scrollToBottom = vi.spyOn(controller, "scrollToBottom").mockImplementation(() => {});

    controller.updateStreamingToolCard(card, { callId: "call-tool", name: "shell", ok: true, output: "done" });

    expect(scrollToBottom).toHaveBeenCalled();
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

  it("rehydrates a sealed card and drawer from the durable run when tool output uses terminal projection", () => {
    const controller = new AgentConversationController({
      shell: { agentConversations: { setActiveSubagentRun: vi.fn().mockResolvedValue(undefined) } },
    } as never);
    controller.conversation = {
      id: "conv-terminal",
      messages: [],
      subagentRuns: [{
        id: "run-real",
        conversationId: "conv-terminal",
        sourceMessageId: "message-1",
        runId: "182c7f25-9eee-4e6e-b927-42436a0c1389",
        providerId: "cursor",
        title: "Test 2: sub agent halo check",
        prompt: "Say hello",
        status: "ok",
        summary: "Halo! Sub agent reporting for duty.",
        steps: [{ type: "text", stepPosition: 1, content: "Full durable ACP response." }],
        createdAt: "2026-08-09T00:00:00.000Z",
        updatedAt: "2026-08-09T00:00:01.000Z",
      }],
    } as never;

    const card = controller.createSubagentToolCard({
      id: "subagent:0",
      name: "subagent",
      ok: true,
      args: { title: "Test 2: sub agent halo check", prompt: "Say hello" },
      output: [
        "status=success",
        "truncated=false",
        "",
        "ok=true",
        "runId=182c7f25-9eee-4e6e-b927-42436a0c1389",
        "providerId=cursor",
        "summary=\"Halo! Sub agent reporting for duty.\"",
      ].join("\n"),
    });
    document.body.appendChild(card);

    expect(card.dataset.runId).toBe("182c7f25-9eee-4e6e-b927-42436a0c1389");
    expect(card.textContent).toContain("Halo! Sub agent reporting for duty.");

    card.querySelector(".agent-subagent-card-head")?.dispatchEvent(new Event("click"));

    expect(document.querySelector("#agent-subpane-body")?.textContent).toContain("Full durable ACP response.");
  });

  it("does not pull a reader back to the bottom after they scroll up", () => {
    const controller = new AgentConversationController({} as never);
    const body = document.createElement("div");
    body.id = "agent-subpane-body";
    document.body.appendChild(body);
    Object.defineProperties(body, {
      scrollHeight: { configurable: true, value: 1000 },
      clientHeight: { configurable: true, value: 200 },
      scrollTop: { configurable: true, writable: true, value: 300 },
    });
    controller.subpaneShouldStickToBottom = false;

    controller.scrollSubpaneToBottom();

    expect(body.scrollTop).toBe(300);
  });

  it("keeps subagent permission decisions in the main attention stack", async () => {
    const answerAcpPermission = vi.fn().mockResolvedValue(undefined);
    const controller = new AgentConversationController({ answerAcpPermission } as never);
    const card = controller.createAcpPermissionCard({
      requestId: "permission-1",
      conversationId: "subagent:run-a",
      toolTitle: "Allow file edit",
      detail: "The subagent wants to edit a file.",
      options: [{ optionId: "allow_once", name: "Allow once" }, { optionId: "reject", name: "Reject" }],
    });

    controller.mountAcpAttentionCard(card!);
    expect(document.querySelector("#agent-attention-stack")?.hidden).toBe(false);
    expect(document.querySelector("#agent-attention-list .acp-permission-card")).toBe(card);

    await controller.submitAcpPermissionCard(card!, "trace-a", "subagent:run-a", "allow_once");
    controller.appendSubpaneText("More subagent output");

    expect(card?.isConnected).toBe(false);
    expect(document.querySelector("#agent-attention-list .acp-permission-card")).toBeNull();
    expect(document.querySelector("#agent-attention-stack")?.hidden).toBe(true);
    expect(answerAcpPermission).toHaveBeenCalledWith({
      traceId: "trace-a",
      conversationId: "subagent:run-a",
      requestId: "permission-1",
      optionId: "allow_once",
    });
  });

  it("seals (not removes) a completed successful subagent card in the active thread UI", () => {
    const controller = new AgentConversationController({} as never);
    const card = controller.createStreamingToolCard("call-subagent", "subagent", { title: "Done" });
    document.body.appendChild(card);

    const replacement = controller.updateStreamingToolCard(card, {
      callId: "call-subagent",
      name: "subagent",
      ok: true,
      output: JSON.stringify({ ok: true, runId: "run-done", summary: "Finished" }),
    });

    // Ticket #42: success must keep a sealed card (replaces the streaming card),
    // not remove it from the DOM.
    expect(replacement).not.toBeNull();
    expect(card.isConnected).toBe(false);
    expect(document.body.querySelector(".agent-subagent-card")).not.toBeNull();
    expect(document.body.querySelector(".agent-subagent-card-status")?.textContent).toMatch(/OK/i);
    expect(document.body.querySelector(".agent-subagent-card")?.textContent).toContain("Finished");
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

  it("persists a subagent run on its parent conversation, not its ACP session", async () => {
    const upsertSubagentRun = vi.fn().mockResolvedValue({ id: "parent-conversation", messages: [] });
    const setActiveSubagentRun = vi.fn().mockResolvedValue({ id: "parent-conversation", messages: [] });
    const controller = new AgentConversationController({
      shell: { agentConversations: { upsertSubagentRun, setActiveSubagentRun } },
    } as never);
    controller.conversation = { id: "other-conversation", messages: [] } as never;

    controller.handleSubagentRunStarted({
      runId: "run-parented",
      conversationId: "subagent:run-parented",
      parentConversationId: "parent-conversation",
      providerId: "cursor",
      prompt: "Fix the regression",
    });
    await Promise.resolve();
    await Promise.resolve();

    expect(upsertSubagentRun).toHaveBeenCalledWith(
      "parent-conversation",
      expect.objectContaining({ conversationId: "parent-conversation", runId: "run-parented" }),
    );
    expect(setActiveSubagentRun).toHaveBeenCalledWith("parent-conversation", "run-parented");
  });

  it("uses the active chat for a legacy event that only has an ACP session id", async () => {
    const upsertSubagentRun = vi.fn().mockResolvedValue({ id: "parent-conversation", messages: [] });
    const setActiveSubagentRun = vi.fn().mockResolvedValue({ id: "parent-conversation", messages: [] });
    const controller = new AgentConversationController({
      shell: { agentConversations: { upsertSubagentRun, setActiveSubagentRun } },
    } as never);
    controller.conversation = { id: "parent-conversation", messages: [] } as never;

    controller.handleSubagentRunStarted({
      runId: "legacy-run",
      conversationId: "subagent:legacy-run",
      providerId: "cursor",
      prompt: "Fix the regression",
    });
    await Promise.resolve();
    await Promise.resolve();

    expect(upsertSubagentRun).toHaveBeenCalledWith(
      "parent-conversation",
      expect.objectContaining({ conversationId: "parent-conversation", runId: "legacy-run" }),
    );
  });

  it("keeps mini streams on separate cards for two concurrent subagents", () => {
    const controller = new AgentConversationController({} as never);
    const first = controller.renderSubagentCard({ runId: "call-one", providerId: "cursor", title: "One", status: "running" });
    first.dataset.streamingSubagent = "1";
    const second = controller.renderSubagentCard({ runId: "call-two", providerId: "gemini", title: "Two", status: "running" });
    second.dataset.streamingSubagent = "1";
    document.body.append(first, second);

    controller.attachSubagentCardStream("run-one");
    controller.attachSubagentCardStream("run-two");

    expect(first.dataset.runId).toBe("run-one");
    expect(second.dataset.runId).toBe("run-two");
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

  it("disposes the mini stream state and seals the card on run end", () => {
    const controller = new AgentConversationController({} as never);
    const card = controller.renderSubagentCard({ runId: "run-f", providerId: "cursor", title: "T", status: "running" });
    document.body.appendChild(card);
    controller.attachSubagentCardStream("run-f");
    controller.appendCardStreamText("frozen tail");

    controller.handleSubagentRunEnded({ runId: "run-f", ok: true, summary: "Done" });

    expect(controller.activeSubagentCardStream).toBeNull();
    expect(card.querySelector(".agent-subagent-card-status")?.textContent).toBe("● OK");
    expect(card.querySelector(".agent-subagent-card-stream")).toBeNull();
    expect(card.querySelector(".agent-subagent-card-summary")?.textContent).toContain("Done");
  });

  it("seals the in-chat card when the subagent lifecycle reports completion", () => {
    const controller = new AgentConversationController({} as never);
    const card = controller.renderSubagentCard({ runId: "run-ended", providerId: "cursor", title: "Audit", status: "running" });
    card.dataset.streamingSubagent = "1";
    document.body.appendChild(card);

    controller.handleSubagentRunEnded({ runId: "run-ended", ok: true, summary: "Audit complete" });

    expect(card.querySelector(".agent-subagent-card-status")?.textContent).toBe("● OK");
    expect(card.querySelector(".agent-subagent-card-status")?.classList.contains("is-ok")).toBe(true);
    expect(card.querySelector(".agent-subagent-card-stream")).toBeNull();
    expect(card.querySelector(".agent-subagent-card-summary")?.textContent).toContain("Audit complete");
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

  // Ticket #42: a successful subagent run must not remove the card from the
  // thread — it becomes a sealed "● OK" card (like terminal tool cards).
  it("createSubagentToolCard returns a non-null sealed card on success (ok)", () => {
    const controller = new AgentConversationController({} as never);
    const card = controller.createSubagentToolCard({
      id: "call-ok",
      name: "subagent",
      ok: true,
      args: { title: "Web audit" },
      output: { ok: true, runId: "run-ok", providerId: "cursor", summary: "Fixed 3 issues." },
    });

    expect(card).not.toBeNull();
    expect(card?.classList.contains("agent-subagent-card")).toBe(true);
    expect(card?.querySelector(".agent-subagent-card-status")?.textContent).toMatch(/OK/i);
    // Summary is rendered so the thread keeps a record of the run.
    expect(card?.textContent).toContain("Fixed 3 issues.");
  });

  it("uses the successful parent tool result when a subagent payload omits ok", () => {
    const controller = new AgentConversationController({} as never);
    const card = controller.createSubagentToolCard({
      id: "call-parent-ok",
      name: "subagent",
      ok: true,
      args: { title: "Web audit" },
      output: { runId: "run-parent-ok", providerId: "cursor" },
    });

    expect(card?.querySelector(".agent-subagent-card-status")?.textContent).toBe("● OK");
  });

  it("updateStreamingToolCard replaces (not removes) a successful subagent card", () => {
    const controller = new AgentConversationController({} as never);
    const parent = document.createElement("div");
    document.body.appendChild(parent);
    // Simulate a streaming subagent card in the DOM.
    const live = document.createElement("div");
    live.className = "agent-subagent-card";
    live.dataset.streamingSubagent = "1";
    live.dataset.callId = "call-x";
    live._toolArgs = { title: "Web audit" };
    parent.appendChild(live);

    const replaced = controller.updateStreamingToolCard(live, {
      callId: "call-x",
      ok: true,
      output: { ok: true, runId: "run-x", providerId: "cursor", summary: "Done." },
    });

    // The original streaming card was replaced by a sealed card, not removed.
    expect(parent.contains(live)).toBe(false);
    expect(parent.querySelector(".agent-subagent-card")).not.toBeNull();
    expect(parent.querySelector(".agent-subagent-card-status")?.textContent).toMatch(/OK/i);
    expect(replaced).not.toBeNull();
  });

  it("keeps a lifecycle-sealed subagent card intact when the parent tool result arrives later", () => {
    const controller = new AgentConversationController({} as never);
    const card = controller.createStreamingToolCard("call-late-tool", "subagent", {
      prompt: "Say hello",
      title: "Greeting",
      provider_id: "cursor",
    });
    card.dataset.runId = "run-lifecycle-first";
    document.body.appendChild(card);

    controller.sealInChatSubagentCard(
      "run-lifecycle-first",
      "ok",
      "Hello! Subagent reporting for duty.",
      undefined,
    );
    const sealed = controller.updateStreamingToolCard(card, {
      callId: "call-late-tool",
      name: "subagent",
      ok: true,
      output: "status=success\ntruncated=false\n\nsummary=Hello",
    });

    expect(sealed).toBe(card);
    expect(card.dataset.runId).toBe("run-lifecycle-first");
    expect(card.querySelector(".agent-subagent-card-summary")?.textContent)
      .toContain("Hello! Subagent reporting for duty.");
  });

  it("keeps the real run id when parent tool completion precedes run_ended so persisted steps open in the drawer", async () => {
    const persistedRun = {
      id: "run-run-tool-first",
      conversationId: "conv-tool-first",
      sourceMessageId: "message-1",
      runId: "run-tool-first",
      providerId: "cursor",
      title: "Greeting",
      prompt: "Say hello",
      status: "ok",
      summary: "Hello from summary",
      steps: [{ type: "text", content: "Hello! Subagent reporting for duty." }],
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    };
    const updateSubagentRunStatus = vi.fn().mockResolvedValue({
      id: "conv-tool-first",
      messages: [],
      subagentRuns: [persistedRun],
    });
    const controller = new AgentConversationController({
      shell: {
        agentConversations: {
          updateSubagentRunStatus,
          setActiveSubagentRun: vi.fn().mockResolvedValue({
            id: "conv-tool-first",
            messages: [],
            subagentRuns: [persistedRun],
          }),
        },
      },
    } as never);
    controller.conversation = { id: "conv-tool-first", messages: [] } as never;
    controller.activeSubagentRun = {
      runId: "run-tool-first",
      providerId: "cursor",
      title: "Greeting",
      prompt: "Say hello",
      status: "running",
      steps: [],
    } as never;
    controller.subagentOwnerConversationId = "conv-tool-first";
    controller.resetSubagentStreamState([], "run-tool-first");
    controller.appendSubpaneText("Hello! Subagent reporting for duty.");

    const live = controller.createStreamingToolCard("call-tool-first", "subagent", {
      prompt: "Say hello",
      title: "Greeting",
      provider_id: "cursor",
    });
    live.dataset.runId = "run-tool-first";
    document.body.appendChild(live);
    const sealed = controller.updateStreamingToolCard(live, {
      callId: "call-tool-first",
      name: "subagent",
      ok: true,
      output: "status=success\ntruncated=false\n\nsummary=Hello",
    });

    expect(sealed?.dataset.runId).toBe("run-tool-first");
    controller.handleSubagentRunEnded({
      runId: "run-tool-first",
      ok: true,
      summary: "Hello from summary",
    });
    await Promise.resolve();
    await Promise.resolve();
    sealed?.querySelector(".agent-subagent-card-head")?.dispatchEvent(new Event("click"));

    expect(document.querySelector("#agent-subpane-body")?.textContent)
      .toContain("Hello! Subagent reporting for duty.");
    expect(sealed?.querySelector(".agent-subagent-card-summary")?.textContent)
      .toContain("Hello from summary");
  });
});
