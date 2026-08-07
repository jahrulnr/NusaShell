// @vitest-environment jsdom

import { describe, expect, it, vi } from "vitest";

// Minimal DOM stubs for the controller (no full message rendering).
function installDom() {
  document.body.innerHTML = `
    <form id="agent-form"></form>
    <textarea id="agent-input"></textarea>
    <button id="agent-send-btn"></button>
    <button id="agent-stop-btn"></button>
    <div id="agent-thread"></div>
    <div id="agent-conversation-list"></div>
    <div id="agent-conversation-count"></div>
  `;
  globalThis.$ = (selector: string) => document.querySelector(selector);
  window.matchMedia = vi.fn(() => ({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() })) as never;
}

import { AgentConversationController } from "../src/renderer/agent-conversation-controller.js";

describe("AgentConversationController — stream lifecycle cleanup (ticket #34)", () => {
  it("schedules a streaming paint via rAF and cancels it, zeroing the id on the streamState", () => {
    installDom();
    // Provide no-op window rAF/cancel so ids are deterministic and no frame
    // ever fires in the test environment.
    const raf = vi.spyOn(window, "requestAnimationFrame").mockImplementation(() => 42 as never);
    const caf = vi.spyOn(window, "cancelAnimationFrame").mockImplementation(() => {});
    const controller = new AgentConversationController({} as never);
    controller.conversation = { id: "c1", messages: [] } as never;
    const streamState: any = { rafIdText: 0 };

    const id = controller.scheduleStreamingPaint(streamState, "text", () => {});
    expect(id).toBe(42);
    expect(raf).toHaveBeenCalledTimes(1);

    controller.cancelStreamingPaint(streamState, "text");
    expect(caf).toHaveBeenCalledWith(42);
    expect(streamState.rafIdText).toBe(0);
    raf.mockRestore();
    caf.mockRestore();
  });

  it("clears a pending canvasRenderTimer and resets it via disposeStreamingCadence", () => {
    installDom();
    const clear = vi.spyOn(window, "clearTimeout").mockImplementation(() => {});
    const controller = new AgentConversationController({} as never);
    const streamState: any = { canvasRenderTimer: 77 };

    controller.disposeStreamingCadence(streamState);
    expect(clear).toHaveBeenCalledWith(77);
    expect(streamState.canvasRenderTimer).toBe(0);
    clear.mockRestore();
  });

  it("guards stale streaming paint from painting into a detached message (canPaint)", () => {
    installDom();
    const controller = new AgentConversationController({} as never);
    const streamState: any = {
      message: { isConnected: true },
      rafIdText: 9,
      rafIdReasoning: 10,
      canvasRenderTimer: 5,
    };
    controller.disposeStreamingCadence(streamState);
    expect(streamState.rafIdText).toBe(0);
    expect(streamState.rafIdReasoning).toBe(0);
    expect(streamState.canvasRenderTimer).toBe(0);
  });

  it("resets pending paint flags when a room switch cancels the frame", () => {
    installDom();
    const controller = new AgentConversationController({} as never);
    const streamState: any = {
      rafIdText: 77,
      rafIdReasoning: 78,
      textRenderPending: true,
      reasoningRenderPending: true,
    };

    controller.disposeStreamingCadence(streamState);

    expect(streamState.textRenderPending).toBe(false);
    expect(streamState.reasoningRenderPending).toBe(false);
  });
});
