// @vitest-environment jsdom
/**
 * Ticket #44 — Stop button "hardness" during live streaming.
 * Once the user clicks Stop:
 *   1. No further delta/tool-call is PAINTED (only accumulated) while the
 *      backend cancel is still settling — stop must be immediate in the
 *      renderer, not gated on the async cancel.
 *   2. The status shows "Stopping…" right away and resets when done.
 *   3. Rapid double-click only issues ONE cancel request (idempotent).
 *   4. The stop flag is cleared when the turn finally settles so a new turn
 *      in the same conversation can stream normally.
 */

import { beforeEach, describe, expect, it, vi } from "vitest";
import { AgentConversationController } from "../src/renderer/agent-conversation-controller.js";

function installDom() {
  document.body.innerHTML = `
    <div id="agent-thread"></div>
    <div id="agent-conversation-list"></div>
    <span id="agent-conversation-count"></span>
    <input id="agent-conversation-search" value="">
    <input id="agent-input">
    <div id="agent-attachments"></div>
    <button id="agent-send-btn"></button>
    <button id="agent-stop-btn" hidden></button>
    <span id="agent-provider-status"></span>
  `;
  globalThis.$ = (selector: string) => document.querySelector(selector);
  window.matchMedia = vi.fn(() => ({
    matches: true,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  })) as never;
}

interface Rooms {
  [id: string]: {
    id: string;
    kind: string;
    messages: unknown[];
  };
}

function makeController(opts: {
  runTurn?: (...args: unknown[]) => Promise<unknown>;
  cancelTurn?: (...args: unknown[]) => Promise<unknown>;
  rooms?: Rooms;
} = {}) {
  const rooms = opts.rooms ?? {
    "room-a": { id: "room-a", kind: "agent", messages: [{ role: "user", content: "hi" }] },
  } as Rooms;
  const get = vi.fn(async (id: string) => rooms[id] ?? null);
  const list = vi.fn(async () =>
    Object.values(rooms).map((c) => ({ id: c.id, kind: c.kind, messageCount: c.messages.length })),
  );
  const append = vi.fn(async (id: string, message: unknown) => {
    const c = rooms[id];
    if (!c) return null;
    const next = { ...c, messages: [...c.messages, message] };
    rooms[id] = next;
    return next;
  });
  const cancelTurn = opts.cancelTurn ?? vi.fn(async () => undefined);
  const controller = new AgentConversationController({
    shell: { agentConversations: { get, list, append, create: vi.fn(async () => rooms["room-a"]) } },
    runTurn: opts.runTurn,
    cancelTurn,
    getActiveModel: () => null,
    log: vi.fn(),
  } as never);
  return { controller, rooms, get, list, append, cancelTurn };
}

describe("AgentConversationController — Stop button hardness (ticket #44)", () => {
  beforeEach(() => installDom());

  it("sets a per-conversation stop flag when stop() is requested", async () => {
    const { controller } = makeController();
    controller.conversation = { id: "room-a", messages: [] } as never;
    controller.activeId = "room-a";
    controller.pendingTurnConversations.add("room-a");
    controller.activeTraceIds.set("room-a", "trace-a");
    const cancel = vi.fn(async () => {
      expect(controller.isStopRequested("room-a")).toBe(true);
    });
    controller.cancelTurn = cancel;

    await controller.stop();

    expect(cancel).toHaveBeenCalledTimes(1);
  });

  it("stops painting further deltas after stop() while still accumulating streamed text", async () => {
    let streamOptions: Record<string, any> | null = null;
    let resolveTurn: ((v: unknown) => void) | null = null;
    const runTurn = vi.fn(async (_messages: unknown, options: Record<string, any>) => {
      streamOptions = options;
      return await new Promise((resolve) => { resolveTurn = resolve; });
    });
    const { controller } = makeController({ runTurn });
    controller.conversation = {
      id: "room-a",
      messages: [{ role: "user", content: "hi" }],
      kind: "agent",
    } as never;
    controller.activeId = "room-a";
    controller.refresh = vi.fn(async () => {});
    const inputEl = document.querySelector<HTMLInputElement>("#agent-input")!;
    inputEl.value = "hello";

    const submitPromise = controller.submit();
    await vi.waitFor(() => expect(streamOptions).not.toBeNull());

    // A text bubble is painted before stop.
    streamOptions!.onDelta("Hello");
    const liveState = controller.liveStreamStates.get("room-a");
    expect(liveState?.streamedText).toBe("Hello");
    expect(document.querySelector(".agent-bubble")).not.toBeNull();

    // Stop: any deltas arriving while the cancel settles must NOT be painted.
    controller.cancelTurn = vi.fn(async () => { await new Promise((r) => setTimeout(r, 20)); });
    const stopPromise = controller.stop();

    streamOptions!.onDelta(" world");
    streamOptions!.onToolCallStart({ callId: "c1", name: "docs_search", args: {} });
    streamOptions!.onToolCallEnd({ callId: "c1", name: "docs_search", ok: true });

    // Accumulated, but not painted: no tool card was appended after stop
    // (onToolCallStart appends synchronously when canPaint is true), and the
    // streamed text grew without rendering new DOM.
    expect(liveState?.streamedText).toBe("Hello world");
    expect(document.querySelector("[data-call-id]")).toBeNull();

    await stopPromise;
    resolveTurn!({ traceId: "trace-a", text: "Hello world", toolCalls: [], rounds: 1 });
    await submitPromise;
  });

  it("shows Stopping… immediately; resets status when cancel settles and flag when turn ends", async () => {
    let streamOptions: Record<string, any> | null = null;
    let resolveTurn: ((v: unknown) => void) | null = null;
    let resolveCancel: ((v: unknown) => void) | null = null;
    const runTurn = vi.fn(async (_messages: unknown, options: Record<string, any>) => {
      streamOptions = options;
      return await new Promise((resolve) => { resolveTurn = resolve; });
    });
    const cancelTurn = vi.fn(async () => new Promise((resolve) => { resolveCancel = resolve; }));
    const { controller } = makeController({ runTurn, cancelTurn });
    controller.conversation = {
      id: "room-a",
      messages: [{ role: "user", content: "hi" }],
      kind: "agent",
    } as never;
    controller.activeId = "room-a";
    controller.refresh = vi.fn(async () => {});
    const inputEl = document.querySelector<HTMLInputElement>("#agent-input")!;
    inputEl.value = "hello";
    const submitPromise = controller.submit();
    await vi.waitFor(() => expect(streamOptions).not.toBeNull());

    const status = document.querySelector("#agent-provider-status")!;
    const stopPromise = controller.stop();
    expect(status.textContent).toBe("Stopping…");
    expect(controller.isStopRequested("room-a")).toBe(true);

    // Cancel settles → visual feedback resets, but the paint gate stays armed
    // until the owning turn's finally runs.
    resolveCancel!(undefined);
    await stopPromise;
    expect(status.textContent).toBe("Idle");
    expect(controller.isStopRequested("room-a")).toBe(true);

    // Turn finally clears the gate → a new turn streams normally.
    resolveTurn!({ traceId: "trace-a", text: "", toolCalls: [], rounds: 1 });
    await submitPromise;
    expect(controller.isStopRequested("room-a")).toBe(false);
  });

  it("is idempotent — a second click while stopping issues no second cancel", async () => {
    let streamOptions: Record<string, any> | null = null;
    let resolveTurn: ((v: unknown) => void) | null = null;
    let resolveCancel: ((v: unknown) => void) | null = null;
    const runTurn = vi.fn(async (_messages: unknown, options: Record<string, any>) => {
      streamOptions = options;
      return await new Promise((resolve) => { resolveTurn = resolve; });
    });
    const cancelTurn = vi.fn(async () => new Promise((resolve) => { resolveCancel = resolve; }));
    const { controller } = makeController({ runTurn, cancelTurn });
    controller.conversation = {
      id: "room-a",
      messages: [{ role: "user", content: "hi" }],
      kind: "agent",
    } as never;
    controller.activeId = "room-a";
    controller.refresh = vi.fn(async () => {});
    const inputEl = document.querySelector<HTMLInputElement>("#agent-input")!;
    inputEl.value = "hello";
    const submitPromise = controller.submit();
    await vi.waitFor(() => expect(streamOptions).not.toBeNull());

    const first = controller.stop();
    const second = controller.stop();

    expect(cancelTurn).toHaveBeenCalledTimes(1);
    resolveCancel!(undefined);
    await Promise.all([first, second]);
    expect(cancelTurn).toHaveBeenCalledTimes(1);

    resolveTurn!({ traceId: "trace-a", text: "", toolCalls: [], rounds: 1 });
    await submitPromise;
  });

  it("clears the stop flag in the turn finally so a new turn can stream", async () => {
    let streamOptions: Record<string, any> | null = null;
    let resolveTurn: ((v: unknown) => void) | null = null;
    const runTurn = vi.fn(async (_messages: unknown, options: Record<string, any>) => {
      streamOptions = options;
      return await new Promise((resolve) => { resolveTurn = resolve; });
    });
    const { controller } = makeController({ runTurn });
    controller.conversation = {
      id: "room-a",
      messages: [{ role: "user", content: "hi" }],
      kind: "agent",
    } as never;
    controller.activeId = "room-a";
    controller.refresh = vi.fn(async () => {});
    const inputEl = document.querySelector<HTMLInputElement>("#agent-input")!;
    inputEl.value = "hello";

    const submitPromise = controller.submit();
    await vi.waitFor(() => expect(streamOptions).not.toBeNull());
    controller.cancelTurn = vi.fn(async () => undefined);

    await controller.stop();
    expect(controller.isStopRequested("room-a")).toBe(true);

    resolveTurn!({ traceId: "trace-a", text: "", toolCalls: [], rounds: 1 });
    await submitPromise;

    expect(controller.isStopRequested("room-a")).toBe(false);
  });
});
