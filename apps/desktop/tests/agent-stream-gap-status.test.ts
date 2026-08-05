// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../src/renderer/ws-client.js", () => ({
  initWsClient: vi.fn(),
  connectWs: vi.fn(),
  sendRequest: vi.fn(),
  subscribe: vi.fn().mockResolvedValue(undefined),
  isConnected: vi.fn(() => true),
  onEvent: vi.fn(() => () => {}),
}));

import { AgentConversationController } from "../src/renderer/agent-conversation-controller.js";

function installDom() {
  document.body.innerHTML = `
    <input id="agent-input">
    <button id="agent-send-btn"></button>
    <button id="agent-stop-btn"></button>
    <span class="agent-provider-status" id="agent-provider-status">Choose a model</span>
    <div id="agent-thread"></div>
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

describe("AgentConversationController — stream gap status surface", () => {
  beforeEach(() => installDom());

  it("shows a stream-incomplete status line when onStreamGap fires", () => {
    const controller = new AgentConversationController({} as never);
    const status = document.querySelector<HTMLSpanElement>("#agent-provider-status")!;
    const originalText = status.textContent;
    controller.activeTraceId = "trace-gap-1";
    controller.pendingTurnConversations.add("test-turn");

    // Invoke the agent-path onStreamGap handler directly.
    // We simulate the gap by calling the internal surface method.
    controller.surfaceStreamGap("trace-gap-1", 5);

    expect(status.textContent).toContain("Stream incomplete");
    expect(status.classList.contains("is-stream-gap")).toBe(true);
  });

  it("clears the stream-gap status after reconcile", () => {
    const controller = new AgentConversationController({} as never);
    controller.getActiveModel = () => ({ key: "m1", contextWindow: 8000 } as never);
    const status = document.querySelector<HTMLSpanElement>("#agent-provider-status")!;
    controller.activeTraceId = "trace-gap-2";
    controller.pendingTurnConversations.add("test-turn");

    controller.surfaceStreamGap("trace-gap-2", 3);
    expect(status.classList.contains("is-stream-gap")).toBe(true);

    controller.clearStreamGapStatus();
    expect(status.classList.contains("is-stream-gap")).toBe(false);
    expect(status.textContent).not.toContain("Stream incomplete");
  });

  it("does not surface gap for a different traceId than the active turn", () => {
    const controller = new AgentConversationController({} as never);
    const status = document.querySelector<HTMLSpanElement>("#agent-provider-status")!;
    controller.activeTraceId = "trace-active";
    controller.pendingTurnConversations.add("test-turn");

    controller.surfaceStreamGap("trace-other", 1);
    expect(status.classList.contains("is-stream-gap")).toBe(false);
  });
});
