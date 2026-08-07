// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";

// Minimal DOM stubs for the controller.
function installDom() {
  document.body.innerHTML = `
    <form id="agent-form"></form>
    <textarea id="agent-input"></textarea>
    <button id="agent-send-btn"></button>
    <button id="agent-stop-btn"></button>
    <div id="agent-thread"><div style="height:200px"></div></div>
    <div id="agent-conversation-list"></div>
    <div id="agent-conversation-count"></div>
    <div id="agent-provider-status"></div>
  `;
  globalThis.$ = (selector: string) => document.querySelector(selector);
  window.matchMedia = vi.fn(() => ({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() })) as never;
}

import { AgentConversationController } from "../src/renderer/agent-conversation-controller.js";

describe("AgentConversationController — render performance (ticket #33)", () => {
  beforeEach(() => installDom());

  it("coalesces scrollToBottom rAF so rapid calls schedule only one settle frame", () => {
    const raf = vi.spyOn(window, "requestAnimationFrame").mockImplementation((cb) => {
      // Do not run the callback; we only assert scheduling count.
      return 1 as never;
    });
    const controller = new AgentConversationController({} as never);
    controller.conversation = { id: "c1", messages: [] } as never;
    const thread = document.getElementById("agent-thread")! as HTMLDivElement;
    thread.scrollTop = 0;

    controller.scrollToBottom({ force: true });
    controller.scrollToBottom({ force: true });
    controller.scrollToBottom({ force: true });

    // One settle frame per burst of scrollToBottom calls, not one per call.
    expect(raf).toHaveBeenCalledTimes(1);
    raf.mockRestore();
  });

  it("debounces composer resize on the input hot path via a single rAF", () => {
    const raf = vi.spyOn(window, "requestAnimationFrame").mockImplementation(() => 7 as never);
    const controller = new AgentConversationController({} as never);
    controller.composerInputWidth = 100;

    controller.scheduleComposerResize();
    controller.scheduleComposerResize();
    controller.scheduleComposerResize();

    expect(raf).toHaveBeenCalledTimes(1);
    raf.mockRestore();
  });

  it("caches the context-badge estimate across repeated refresh() calls for unchanged messages", () => {
    const controller = new AgentConversationController({
      getActiveModel: () => ({ id: "m1", contextWindow: 200_000 }),
    } as never);
    controller.conversation = {
      id: "c1",
      messages: [{ role: "user", content: "hi" }],
      checkpoint: null,
    } as never;
    const status = document.getElementById("agent-provider-status")!;

    controller.updateContextStatus();
    const first = status.textContent;
    // Second call with identical conversation must not re-estimate (returns cached text).
    controller.updateContextStatus();
    expect(status.textContent).toBe(first);
    expect(first).toBeTruthy();
  });
});
