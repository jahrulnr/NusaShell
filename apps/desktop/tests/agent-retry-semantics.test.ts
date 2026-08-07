// @vitest-environment jsdom
/**
 * Ticket #45 — Retry button semantics: error classification, semantic labels
 * (Retry/Resume/Continue), and rate-limit backoff/cooldown.
 */

import { beforeEach, describe, expect, it, vi } from "vitest";
import { AgentConversationController } from "../src/renderer/agent-conversation-controller.js";
import { classifyTurnError } from "../src/renderer/agent-conversation-ui.js";

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

function makeController(extra: Record<string, unknown> = {}) {
  const controller = new AgentConversationController({
    shell: { agentConversations: {} },
    getActiveModel: () => null,
    log: vi.fn(),
    ...extra,
  } as never);
  return { controller };
}

describe("AgentConversationController — Retry semantics (ticket #45)", () => {
  beforeEach(() => installDom());

  it("shows a semantic Resume label for an interrupted tool turn", async () => {
    const { controller } = makeController();
    controller.conversation = {
      id: "c1",
      messages: [{ role: "user", content: "hi" }],
    } as never;
    const saved = { id: "s1", createdAt: "2026-01-01T00:00:00.000Z" };
    const message = controller.appendMessage("assistant", "Turn interrupted · ready to resume", {
      error: true,
      retry: true,
      retryLabel: "Resume",
      ...saved,
    });
    const btn = message?.querySelector(".agent-retry-btn");
    expect(btn).not.toBeNull();
    expect(btn?.textContent).toBe("Resume");
  });

  it("renders a Retry button disabled with a countdown for a rate-limited turn", async () => {
    vi.useFakeTimers();
    const { controller } = makeController();
    controller.conversation = { id: "c1", messages: [{ role: "user", content: "hi" }] } as never;
    const message = controller.appendMessage("assistant", "Rate limited — wait a moment and try again", {
      error: true,
      retry: true,
      retryLabel: "Retry",
      retryCooldownMs: 2000,
      createdAt: "2026-01-01T00:00:00.000Z",
    });
    const btn = message?.querySelector(".agent-retry-btn") as HTMLButtonElement;
    expect(btn).not.toBeNull();
    expect(btn.disabled).toBe(true);
    expect(btn.textContent).toMatch(/Retry \(\d+s\)/);

    // After the cooldown elapses the button re-enables.
    vi.advanceTimersByTime(3000);
    expect(btn.disabled).toBe(false);
    expect(btn.textContent).toBe("Retry");
    vi.useRealTimers();
  });

  it("classifies a server 5xx as retryable without a cooldown", () => {
    const cls = classifyTurnError({
      code: "AGENT_PROVIDER_FAILED",
      message: "AI provider request failed: Provider returned HTTP 503: overloaded",
      details: { cause: "Provider returned HTTP 503: overloaded" },
    });
    expect(cls.category).toBe("server_error");
    expect(cls.retryable).toBe(true);
  });

  it("surfaces superseded turns without a misleading retry", async () => {
    const { controller } = makeController();
    controller.conversation = { id: "c1", messages: [{ role: "user", content: "hi" }] } as never;
    const message = controller.appendMessage("assistant", "Turn superseded by a newer turn", {
      error: true,
      retry: false,
      createdAt: "2026-01-01T00:00:00.000Z",
    });
    expect(message?.querySelector(".agent-retry-btn")).toBeNull();
  });
});
