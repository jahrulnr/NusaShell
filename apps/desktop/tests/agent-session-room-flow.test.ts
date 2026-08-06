// @vitest-environment jsdom
/**
 * Unit tests for agent rooms / live turn / subagent / drawers.
 * Mapped from tmp/plan/agent-ui-bh-catalog.md (BH-AGENT-*).
 */

import { beforeEach, describe, expect, it, vi } from "vitest";
import { AgentConversationController } from "../src/renderer/agent-conversation-controller.js";
import {
  describeToolActivity,
  renderReasoningMarkdown,
} from "../src/renderer/agent-conversation-ui.js";

function installDom() {
  document.body.innerHTML = `
    <div id="agent-conversation-list"></div>
    <span id="agent-conversation-count"></span>
    <input id="agent-conversation-search" value="">
    <div id="agent-thread"></div>
    <input id="agent-input">
    <div id="agent-attachments"></div>
    <button id="agent-send-btn"></button>
    <button id="agent-stop-btn" hidden></button>
    <span id="agent-provider-status"></span>
    <span id="agent-workspace-label"></span>
    <button id="agent-workspace-btn"></button>
    <div id="acp-status-bar" hidden>
      <span id="acp-status-provider"></span>
      <span id="acp-status-chip"></span>
    </div>
    <button id="agent-acp-pill" hidden><span id="agent-acp-pill-label"></span></button>
    <div class="agent-canvas-overlay" id="agent-canvas-overlay" hidden></div>
    <aside class="agent-canvas" id="agent-canvas" hidden>
      <div id="agent-canvas-resize" role="separator" tabindex="0"></div>
      <span id="agent-canvas-badge"></span>
      <span id="agent-canvas-title"></span>
      <button id="agent-canvas-close" type="button"></button>
      <button id="agent-canvas-refresh" type="button"></button>
      <button id="agent-canvas-download" type="button"></button>
      <div id="agent-canvas-body"></div>
      <p id="agent-canvas-hint"></p>
    </aside>
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
  window.matchMedia = vi.fn((query: string) => ({
    matches: String(query).includes("prefers-reduced-motion"),
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })) as typeof window.matchMedia;
}

function room(id: string, patch: Record<string, unknown> = {}) {
  return {
    id,
    title: `Room ${id}`,
    kind: "agent",
    createdAt: "2026-01-01T00:00:00.000Z",
    updatedAt: "2026-01-01T00:00:00.000Z",
    messageCount: Array.isArray(patch.messages) ? (patch.messages as unknown[]).length : 0,
    messages: [],
    ...patch,
  };
}

function makeController(opts: {
  rooms?: Map<string, ReturnType<typeof room>>;
  getActiveTurn?: (conversationId: string) => Promise<unknown>;
  getConversation?: (conversationId: string) => Promise<ReturnType<typeof room> | null>;
} = {}) {
  const rooms = opts.rooms ?? new Map([
    ["room-a", room("room-a", { messages: [{ role: "user", content: "hi A" }] })],
    ["room-b", room("room-b", { messages: [{ role: "user", content: "hi B" }] })],
  ]);
  const get = vi.fn(opts.getConversation ?? (async (id: string) => rooms.get(id) ?? null));
  const list = vi.fn(async () =>
    [...rooms.values()].map((c) => ({
      id: c.id,
      title: c.title,
      createdAt: c.createdAt,
      updatedAt: c.updatedAt,
      messageCount: c.messageCount,
      kind: c.kind,
    })),
  );
  const controller = new AgentConversationController({
    shell: {
      agentConversations: {
        get,
        list,
        upsertSubagentRun: vi.fn(async (id: string, run: unknown) => {
          const c = rooms.get(id);
          if (!c) return null;
          const runs = [...(c.subagentRuns ?? []).filter((r: { runId: string }) => r.runId !== (run as { runId: string }).runId), run];
          const next = { ...c, subagentRuns: runs };
          rooms.set(id, next);
          return next;
        }),
        setActiveSubagentRun: vi.fn(async (id: string) => rooms.get(id) ?? null),
        updateSubagentRunStatus: vi.fn(async (id: string) => rooms.get(id) ?? null),
      },
    },
    getActiveModel: () => null,
    getActiveTurn: opts.getActiveTurn,
    log: vi.fn(),
  } as never);
  controller.conversations = [...rooms.values()].map((c) => ({
    id: c.id,
    title: c.title,
    createdAt: c.createdAt,
    updatedAt: c.updatedAt,
    messageCount: c.messageCount,
  })) as never;
  return { controller, rooms, get, list };
}

describe("BH-AGENT room / turn / drawer suite", () => {
  beforeEach(() => installDom());

  it("does not let a stale room load overwrite the latest room selection", async () => {
    let resolveA!: (value: ReturnType<typeof room>) => void;
    const roomA = new Promise<ReturnType<typeof room>>((resolve) => { resolveA = resolve; });
    const { controller } = makeController({
      getConversation: vi.fn((id: string) => id === "room-a" ? roomA : Promise.resolve(room("room-b"))),
    });

    const openingA = controller.open("room-a");
    await controller.open("room-b");
    resolveA(room("room-a"));
    await openingA;

    expect(controller.activeId).toBe("room-b");
    expect(controller.conversation?.id).toBe("room-b");
  });

  it("mounts the todo strip for a newly created room", async () => {
    const conversation = room("room-new");
    const controller = new AgentConversationController({
      shell: { agentConversations: { create: vi.fn(async () => conversation) } },
      deleteTodos: vi.fn(),
    } as never);
    const mountTodoStrip = vi.spyOn(controller, "mountTodoStrip").mockImplementation(() => {});
    controller.renderThread = vi.fn();
    controller.updateWorkspaceLabel = vi.fn();
    controller.updateContextStatus = vi.fn();
    controller.updateAcpStatus = vi.fn();
    controller.refresh = vi.fn(async () => {});

    await controller.create();

    expect(mountTodoStrip).toHaveBeenCalledWith("room-new");
  });

  it("keeps the reader's position while streaming away from the bottom", () => {
    const controller = makeController().controller;
    const thread = document.querySelector("#agent-thread") as HTMLElement;
    Object.defineProperties(thread, {
      clientHeight: { configurable: true, value: 400 },
      scrollHeight: { configurable: true, writable: true, value: 1200 },
    });
    thread.scrollTop = 180;
    controller.threadShouldStickToBottom = false;

    controller.scrollToBottom();

    expect(thread.scrollTop).toBe(180);
  });

  it("continues following streamed content when the reader is at the bottom", () => {
    const controller = makeController().controller;
    const thread = document.querySelector("#agent-thread") as HTMLElement;
    Object.defineProperties(thread, {
      clientHeight: { configurable: true, value: 400 },
      scrollHeight: { configurable: true, writable: true, value: 1200 },
    });
    thread.scrollTop = 800;
    controller.threadShouldStickToBottom = true;
    vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => {
      callback(0);
      return 0;
    });

    controller.scrollToBottom();

    expect(thread.scrollTop).toBe(1200);
    vi.unstubAllGlobals();
  });

  it("follows immediately when animation frames are throttled", () => {
    const controller = makeController().controller;
    const thread = document.querySelector("#agent-thread") as HTMLElement;
    Object.defineProperties(thread, {
      clientHeight: { configurable: true, value: 400 },
      scrollHeight: { configurable: true, writable: true, value: 1600 },
    });
    thread.scrollTop = 1200;
    controller.threadShouldStickToBottom = true;
    vi.stubGlobal("requestAnimationFrame", vi.fn());

    controller.scrollToBottom();

    expect(thread.scrollTop).toBe(1600);
    vi.unstubAllGlobals();
  });

  it("BH-AGENT-01 keeps other room composer free while a background room owns the turn", async () => {
    const { controller } = makeController();
    const input = document.querySelector<HTMLInputElement>("#agent-input")!;
    const send = document.querySelector<HTMLButtonElement>("#agent-send-btn")!;
    const stop = document.querySelector<HTMLButtonElement>("#agent-stop-btn")!;

    controller.pendingTurnConversations.add("room-a");
    controller.turnOwnerConversationId = "room-a";
    controller.conversation = room("room-a") as never;
    controller.activeId = "room-a";
    input.disabled = true;
    send.disabled = true;
    stop.hidden = false;

    await controller.open("room-b");

    expect(controller.activeId).toBe("room-b");
    expect(controller.turnOwnerConversationId).toBe("room-a");
    expect(controller.turnPending).toBe(true);
    expect(input.disabled).toBe(false);
    expect(send.disabled).toBe(false);
    expect(stop.hidden).toBe(true);
  });

  it("BH-AGENT-15 allows submit in room B while room A owns a running turn", async () => {
    const rooms = new Map([
      ["room-a", room("room-a", { messages: [{ role: "user", content: "hi A" }] })],
      ["room-b", room("room-b", { messages: [] })],
    ]);
    const append = vi.fn(async (id: string, msg: unknown) => {
      const c = rooms.get(id);
      if (!c) return null;
      const next = { ...c, messages: [...c.messages, msg] };
      rooms.set(id, next);
      return next;
    });
    const get = vi.fn(async (id: string) => rooms.get(id) ?? null);
    const list = vi.fn(async () =>
      [...rooms.values()].map((c) => ({
        id: c.id, title: c.title, createdAt: c.createdAt, updatedAt: c.updatedAt,
        messageCount: c.messageCount, kind: c.kind,
      })),
    );
    const runTurn = vi.fn(async () => ({
      traceId: "trace-b",
      text: "ok",
      toolCalls: [],
      steps: [],
      rounds: 1,
    }));
    const controller = new AgentConversationController({
      shell: { agentConversations: { get, list, append, create: vi.fn(async () => rooms.get("room-b")!) } },
      runTurn,
      getActiveModel: () => ({ key: "m1", contextWindow: 8000 } as never),
      log: vi.fn(),
    } as never);
    controller.conversations = [...rooms.values()].map((c) => ({
      id: c.id, title: c.title, createdAt: c.createdAt, updatedAt: c.updatedAt,
      messageCount: c.messageCount,
    })) as never;

    // Simulate room A owning a running turn.
    controller.pendingTurnConversations.add("room-a");
    controller.turnOwnerConversationId = "room-a";

    // Switch to room B and type a message.
    await controller.open("room-b");
    const input = document.querySelector<HTMLInputElement>("#agent-input")!;
    input.value = "hello from B";

    // Debug: verify state before submit
    const convId = controller.conversation?.id;
    const isRunning = controller.isConversationRunning(convId);
    const inflight = controller._submitInFlight;
    const textVal = document.querySelector<HTMLInputElement>("#agent-input")?.value;

    // submit() must NOT bail because of room A's pending turn.
    await controller.submit();

    // runTurn was called — submit() proceeded past the guard.
    expect(runTurn).toHaveBeenCalledTimes(1);
    expect(runTurn.mock.calls[0]?.[1]?.conversationId).toBe("room-b");
    // Room B's turn completed and was cleaned up; room A is still running.
    expect(controller.pendingTurnConversations.has("room-b")).toBe(false);
    expect(controller.pendingTurnConversations.has("room-a")).toBe(true);
  });

  it("BH-AGENT-02/03/04/05 rehydrate Working with reasoning, tool, and text after return", async () => {
    const getActiveTurn = vi.fn(async (id: string) => {
      if (id !== "room-a") return null;
      return {
        conversationId: "room-a",
        traceId: "trace-a",
        status: "running",
        steps: [
          { type: "reasoning", content: "Plan the fix carefully." },
          { type: "text", content: "Starting work." },
        ],
        openTools: [
          { id: "call-1", name: "docs_search", status: "running", args: { query: "agent" } },
        ],
        streaming: { kind: "text", content: " more tokens…" },
        updatedAt: new Date().toISOString(),
      };
    });
    const { controller } = makeController({ getActiveTurn });
    controller.pendingTurnConversations.clear();
    await controller.open("room-a");
    await controller.restoreActiveTurnUi();

    expect(controller.turnPending).toBe(true);
    expect(controller.activeTraceId).toBe("trace-a");
    expect(document.querySelector("#agent-stop-btn")?.hidden).toBe(false);

    const host = document.querySelector("#agent-thread article.agent-message.agent-pending");
    expect(host).not.toBeNull();
    expect(host?.querySelector(".agent-message-meta")?.textContent).toMatch(/Working/i);
    expect(host?.querySelector(".agent-reasoning")).not.toBeNull();
    expect(host?.querySelector(".agent-reasoning-content")?.innerHTML).toMatch(/Plan the fix/);
    expect(host?.textContent).toContain("Starting work.");
    expect(host?.textContent).toContain("more tokens");
    // Tool terminal card for open tool
    const toolCard = host?.querySelector("[data-call-id=\"call-1\"]");
    expect(toolCard).not.toBeNull();
    expect(toolCard?.textContent ?? "").toMatch(/docs_search/i);
  });

  it("BH-AGENT-06 restores only the visible room's running subagents", async () => {
    const rooms = new Map([
      ["room-a", room("room-a", {
        messages: [{ role: "user", content: "A" }],
        subagentRuns: [{
          runId: "sub-a",
          providerId: "cursor",
          prompt: "task A",
          status: "running",
          title: "Sub A",
          steps: [],
        }],
      })],
      ["room-b", room("room-b", {
        messages: [{ role: "user", content: "B" }],
        subagentRuns: [{
          runId: "sub-b",
          providerId: "codex",
          prompt: "task B",
          status: "running",
          title: "Sub B",
          steps: [],
        }],
      })],
    ]);
    const { controller } = makeController({ rooms });

    await controller.open("room-a");
    expect(document.querySelector('[data-run-id="sub-a"]')).not.toBeNull();
    expect(document.querySelector('[data-run-id="sub-b"]')).toBeNull();

    await controller.open("room-b");
    expect(document.querySelector('[data-run-id="sub-b"]')).not.toBeNull();
    expect(document.querySelector('[data-run-id="sub-a"]')).toBeNull();
  });

  it("BH-AGENT-07 preserves subpane live text across close/remount", () => {
    const { controller } = makeController();
    const run = {
      runId: "sub-live",
      providerId: "gemini",
      prompt: "x",
      status: "running",
      title: "Live sub",
      steps: [],
    };
    controller.conversation = room("room-a") as never;
    controller.activeSubagentRun = run as never;
    controller.resetSubagentStreamState([], run.runId);
    controller.appendSubpaneText("live drawer body");
    controller.closeSubpaneDrawerUi();
    controller.mountSubpane(run as never, { open: true });
    expect(document.querySelector("#agent-subpane-body")?.textContent).toContain("live drawer body");
  });

  it("BH-AGENT-08 shows Stop when opened room only has a running subagent", async () => {
    const rooms = new Map([
      ["room-b", room("room-b", {
        messages: [{ role: "assistant", content: "done earlier" }],
        subagentRuns: [{
          runId: "sub-only",
          providerId: "cursor",
          prompt: "solo",
          status: "running",
          title: "Solo",
          steps: [],
        }],
      })],
    ]);
    const { controller } = makeController({ rooms });
    controller.pendingTurnConversations.clear();
    await controller.open("room-b");
    await controller.restoreRunningTurnState();
    expect(document.querySelector("#agent-stop-btn")?.hidden).toBe(false);
  });

  it("BH-AGENT-09/10 open and close canvas drawer without leaving body junk", async () => {
    const { controller } = makeController();
    const body = document.querySelector("#agent-canvas-body")!;
    body.textContent = "stale artifact";
    controller.openCanvasDrawerUi();
    // rAF runs class is-open
    await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
    const pane = document.querySelector("#agent-canvas") as HTMLElement;
    const overlay = document.querySelector("#agent-canvas-overlay") as HTMLElement;
    expect(pane.hidden).toBe(false);
    expect(overlay.hidden).toBe(false);

    controller.closeCanvasDrawerUi();
    expect(body.textContent).toBe("");
    expect(pane.classList.contains("is-open")).toBe(false);
    // reduced-motion path hides immediately in installDom
    expect(pane.hidden).toBe(true);
  });

  it("resizes the canvas drawer with keyboard controls and preserves the width", () => {
    const { controller } = makeController();
    controller.bindCanvasControls();
    const pane = document.querySelector<HTMLElement>("#agent-canvas")!;
    const handle = document.querySelector<HTMLElement>("#agent-canvas-resize")!;

    handle.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowLeft", bubbles: true }));
    expect(pane.style.getPropertyValue("--agent-canvas-width")).toBe("584px");
    expect(controller.getCanvasDrawerWidth()).toBe(584);

    handle.dispatchEvent(new KeyboardEvent("keydown", { key: "Home", bubbles: true }));
    expect(controller.getCanvasDrawerWidth()).toBe(960);
    expect(window.localStorage.getItem("nusashell.agent.canvas.drawer-width")).toBe("960");
  });

  it("BH-AGENT-11 paints incomplete turn when trailing user and no parent mutex", () => {
    const { controller } = makeController();
    controller.pendingTurnConversations.clear();
    controller.conversation = room("room-a", {
      messages: [{ role: "user", content: "only user left" }],
    }) as never;
    controller.renderThread();
    const incomplete = document.querySelector("#agent-thread article.agent-message-error");
    expect(incomplete?.textContent).toMatch(/Incomplete turn/i);
    expect(incomplete?.querySelector(".agent-retry-btn, button")).toBeTruthy();
  });

  it("BH-AGENT-12 rehydrate still creates Working when projection is running", async () => {
    const getActiveTurn = vi.fn(async () => ({
      conversationId: "room-a",
      traceId: "t-live",
      status: "running",
      steps: [],
      openTools: [],
      streaming: { kind: "text", content: "still going" },
      updatedAt: new Date().toISOString(),
    }));
    const { controller } = makeController({ getActiveTurn });
    controller.pendingTurnConversations.clear();
    controller.conversation = room("room-a", {
      messages: [{ role: "user", content: "orphan shape" }],
    }) as never;
    controller.activeId = "room-a";
    controller.renderThread();
    await controller.restoreActiveTurnUi();
    expect(controller.turnPending).toBe(true);
    expect(document.querySelector("#agent-thread article.agent-pending")?.textContent).toContain("still going");
    expect(document.querySelector("#agent-thread article.agent-message-error")).toBeNull();
  });

  it("BH-AGENT-13 rejects executable markup in reasoning HTML", () => {
    const html = renderReasoningMarkdown("<img src=x onerror=alert(1)> **ok**");
    expect(html).not.toMatch(/onerror/i);
    expect(html).toContain("ok");
  });

  it("BH-AGENT-14 describes tool activity clearly", () => {
    const summary = describeToolActivity([
      { name: "docs_search", ok: true, args: { query: "x" } },
      { name: "docs_read", ok: false },
    ]);
    expect(summary.label).toMatch(/2 tool calls/);
    expect(summary.succeeded).toBe(1);
    expect(summary.failed).toBe(1);
  });

  // ── Per-conversation state isolation (BH-AGENT-16..21) ──
  // Each test verifies that a field which should be scoped per-conversation
  // does not leak from room A into room B when the user switches rooms.

  it("BH-AGENT-16 isolates activeTraceId per conversation", async () => {
    const { controller } = makeController();
    await controller.open("room-a");
    controller.turnOwnerConversationId = "room-a";
    controller.activeTraceId = "trace-a";
    await controller.open("room-b");
    expect(controller.activeTraceId).toBe("");
    // Switching back to room A should restore room A's traceId.
    await controller.open("room-a");
    expect(controller.activeTraceId).toBe("trace-a");
  });

  it("BH-AGENT-17 isolates liveStreamState per conversation", async () => {
    const { controller } = makeController();
    await controller.open("room-a");
    controller.turnOwnerConversationId = "room-a";
    const streamStateA = { message: {}, textBubble: null, streamedText: "A" };
    controller.liveStreamState = streamStateA;
    await controller.open("room-b");
    expect(controller.liveStreamState).toBeNull();
    await controller.open("room-a");
    expect(controller.liveStreamState).toBe(streamStateA);
  });

  it("keeps background room deltas instead of dropping them during a room switch", async () => {
    const rooms = new Map([
      ["room-a", room("room-a", { messages: [] })],
      ["room-b", room("room-b", { messages: [] })],
    ]);
    let streamOptions: Record<string, any> | null = null;
    let finishTurn: ((result: unknown) => void) | null = null;
    const runTurn = vi.fn(async (_messages: unknown, options: Record<string, any>) => {
      streamOptions = options;
      return await new Promise((resolve) => { finishTurn = resolve; });
    });
    const controller = new AgentConversationController({
      shell: {
        agentConversations: {
          get: vi.fn(async (id: string) => rooms.get(id) ?? null),
          append: vi.fn(async (id: string, message: unknown) => {
            const current = rooms.get(id)!;
            const next = { ...current, messages: [...current.messages, message] };
            rooms.set(id, next);
            return next;
          }),
          list: vi.fn(async () => []),
        },
      },
      runTurn,
      getActiveModel: () => null,
      log: vi.fn(),
    } as never);
    controller.conversation = rooms.get("room-a") as never;
    controller.activeId = "room-a";
    const input = document.querySelector<HTMLInputElement>("#agent-input")!;
    input.value = "start A";
    controller.refresh = vi.fn(async () => {});

    const submitPromise = controller.submit();
    await vi.waitFor(() => expect(streamOptions).not.toBeNull());

    // A continues to stream while B is the visible room.
    controller.conversation = rooms.get("room-b") as never;
    controller.activeId = "room-b";
    document.querySelector("#agent-provider-status")!.textContent = "Room B status";
    streamOptions!.onDelta("first");
    streamOptions!.onDelta(" background");

    const stateA = controller.liveStreamStates.get("room-a");
    expect(stateA?.streamedText).toBe("first background");
    expect(document.querySelector("#agent-provider-status")?.textContent).toBe("Room B status");
    expect(document.querySelector("#agent-thread article.agent-pending .agent-bubble")).toBeNull();

    finishTurn!({ traceId: streamOptions!.traceId, text: "first background", toolCalls: [], steps: [], rounds: 1 });
    await submitPromise;
  });

  it("BH-AGENT-18 isolates autoContinueAborted per conversation", async () => {
    const { controller } = makeController();
    await controller.open("room-a");
    controller.autoContinueAborted = true;
    await controller.open("room-b");
    expect(controller.autoContinueAborted).toBe(false);
  });
});
