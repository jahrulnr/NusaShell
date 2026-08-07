// @vitest-environment jsdom
/**
 * Ticket #46 — Send button & Shift+Enter:
 *   1. IME composition is not handled — Ctrl+Enter false-submits while an IME
 *      candidate is being composed (isComposing / keyCode 229).
 *   2. Send button is not disabled when the input is empty (click = silent no-op).
 *   3. Explicit newline semantics: plain Enter and Shift+Enter must remain
 *      newline (default textarea behavior), never intercepted to submit.
 */

import { beforeEach, describe, expect, it, vi } from "vitest";
import { AgentConversationController } from "../src/renderer/agent-conversation-controller.js";

function installDom() {
  document.body.innerHTML = `
    <form id="agent-form">
      <textarea id="agent-input" rows="1"></textarea>
      <div id="agent-attachments"></div>
      <button id="agent-send-btn" type="submit"></button>
      <button id="agent-stop-btn" hidden></button>
      <span id="agent-provider-status"></span>
    </form>
    <button id="agent-attach-btn" type="button"></button>
    <button id="agent-workspace-btn" type="button"></button>
    <input id="agent-file-input" type="file">
    <button id="agent-new-conversation"></button>
    <button id="agent-delete-close"></button>
    <button id="agent-delete-cancel"></button>
    <button id="agent-delete-confirm"></button>
    <div id="agent-delete-overlay" hidden></div>
    <div id="agent-delete-dialog" hidden></div>
    <div id="agent-conversation-list"></div>
    <div id="agent-conversation-count"></div>
    <input id="agent-conversation-search">
    <div id="agent-conversation-search-wrap"></div>
    <div id="agent-thread"></div>
    <div id="agent-mobile-conversations-btn"></div>
    <div id="agent-mobile-conversations-overlay"></div>
    <div id="agent-room-info-trigger"></div>
    <button id="agent-room-info-close"></button>
    <div id="agent-room-info"></div>
    <button id="agent-workspace-btn"></button>
  `;
  globalThis.$ = (selector: string) => document.querySelector(selector);
  (globalThis as Record<string, unknown>).ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
  window.matchMedia = vi.fn(() => ({
    matches: true,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  })) as never;
}

function makeController() {
  const controller = new AgentConversationController({
    shell: { agentConversations: {} },
    getActiveModel: () => null,
    log: vi.fn(),
  } as never);
  // Avoid touching all of bindEvents' selectors — only the input keydown + send
  // availability matter here. We bind a minimal subset.
  return { controller };
}

describe("AgentConversationController — Send / IME / Shift+Enter (ticket #46)", () => {
  beforeEach(() => installDom());

  it("does not submit on Ctrl+Enter while an IME composition is active", () => {
    const { controller } = makeController();
    controller.conversation = { id: "c1", messages: [] } as never;
    controller.activeId = "c1";
    const submit = vi.spyOn(controller, "submit");
    const input = document.querySelector<HTMLTextAreaElement>("#agent-input")!;
    input.value = "héllo";
    controller.bindEvents();
    input.dispatchEvent(new KeyboardEvent("keydown", {
      key: "Enter",
      ctrlKey: true,
      metaKey: false,
      isComposing: true,
      bubbles: true,
    }));
    expect(submit).not.toHaveBeenCalled();
  });

  it("submits on Ctrl+Enter when not composing", () => {
    const { controller } = makeController();
    controller.conversation = { id: "c1", messages: [] } as never;
    controller.activeId = "c1";
    // Spy on submit but make it a no-op so it doesn't run the full turn flow.
    const submit = vi.spyOn(controller, "submit").mockResolvedValue(undefined as never);
    const input = document.querySelector<HTMLTextAreaElement>("#agent-input")!;
    input.value = "hello";
    controller.bindEvents();
    input.dispatchEvent(new KeyboardEvent("keydown", {
      key: "Enter",
      ctrlKey: true,
      metaKey: false,
      isComposing: false,
      bubbles: true,
    }));
    expect(submit).toHaveBeenCalledTimes(1);
  });

  it("does not intercept plain Shift+Enter (default newline, no submit)", () => {
    const { controller } = makeController();
    controller.conversation = { id: "c1", messages: [] } as never;
    controller.activeId = "c1";
    const submit = vi.spyOn(controller, "submit");
    const input = document.querySelector<HTMLTextAreaElement>("#agent-input")!;
    input.value = "hello";
    controller.bindEvents();
    const evt = new KeyboardEvent("keydown", {
      key: "Enter",
      shiftKey: true,
      ctrlKey: false,
      metaKey: false,
      bubbles: true,
    });
    const defaultPrevented = !input.dispatchEvent(evt);
    expect(submit).not.toHaveBeenCalled();
    // This is an explicit guard that Shift+Enter stays as a newline: the event
    // must not be prevented (which would suppress the default newline).
    expect(defaultPrevented).toBe(false);
  });

  it("disables the send button when the input is empty", () => {
    const { controller } = makeController();
    controller.conversation = { id: "c1", messages: [] } as never;
    controller.activeId = "c1";
    const input = document.querySelector<HTMLTextAreaElement>("#agent-input")!;
    const send = document.querySelector<HTMLButtonElement>("#agent-send-btn")!;
    controller.bindEvents();
    input.value = "";
    controller.updateSendAvailability();
    expect(send.disabled).toBe(true);

    input.value = "typed";
    controller.updateSendAvailability();
    expect(send.disabled).toBe(false);
  });

  it("re-enables the send button after the input is cleared on submit", () => {
    const { controller } = makeController();
    controller.conversation = { id: "c1", messages: [] } as never;
    controller.activeId = "c1";
    const input = document.querySelector<HTMLTextAreaElement>("#agent-input")!;
    const send = document.querySelector<HTMLButtonElement>("#agent-send-btn")!;
    controller.bindEvents();
    input.value = "hello";
    controller.updateSendAvailability();
    expect(send.disabled).toBe(false);

    // Simulate the submit-side clear.
    input.value = "";
    controller.updateSendAvailability();
    expect(send.disabled).toBe(true);
  });

  it("disables send while an IME composition is active on the input", () => {
    const { controller } = makeController();
    controller.conversation = { id: "c1", messages: [] } as never;
    controller.activeId = "c1";
    const input = document.querySelector<HTMLTextAreaElement>("#agent-input")!;
    const send = document.querySelector<HTMLButtonElement>("#agent-send-btn")!;
    controller.bindEvents();
    input.value = "héllo";
    controller.inputComposing = true;
    controller.updateSendAvailability();
    expect(send.disabled).toBe(true);
  });
});
