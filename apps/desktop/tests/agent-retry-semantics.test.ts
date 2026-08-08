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

  it("does not offer retry for non-transient provider 4xx responses", () => {
    const cls = classifyTurnError({
      code: "AGENT_PROVIDER_FAILED",
      message: "AI provider request failed: Provider returned HTTP 402: payment required",
      details: { cause: "Provider returned HTTP 402: payment required" },
    });
    expect(cls.category).toBe("client_error");
    expect(cls.retryable).toBe(false);
    expect(cls.label).toBe("");
  });

  it("clears only the visible room failure action", () => {
    const { controller } = makeController();
    controller.conversation = { id: "room-a", messages: [{ role: "user", content: "a" }] } as never;
    const roomA = controller.appendMessage("assistant", "A failed", { error: true, retry: true });
    controller.conversation = { id: "room-b", messages: [{ role: "user", content: "b" }] } as never;
    const roomB = controller.appendMessage("assistant", "B failed", { error: true, retry: true });

    controller.clearVisibleFailureMessage("room-b");

    expect(roomA?.isConnected).toBe(true);
    expect(roomB?.isConnected).toBe(false);
  });

  it("durably repairs a trailing user message after a renderer restart", async () => {
    const append = vi.fn(async (_conversationId: string, message: unknown) => ({
      id: "room-a",
      messages: [
        { role: "user", content: "unfinished" },
        message,
      ],
    }));
    const controller = makeController({
      shell: {
        agentConversations: {
          get: vi.fn(async () => ({ id: "room-a", messages: [{ role: "user", content: "unfinished" }] })),
          append,
        },
      },
    }).controller;
    controller.conversation = { id: "room-a", messages: [{ role: "user", content: "unfinished" }] } as never;

    controller.detectOrphanedTurn();
    await vi.waitFor(() => expect(append).toHaveBeenCalled());

    expect(append.mock.calls[0][1]).toMatchObject({
      status: "interrupted",
      retryOnly: true,
      interruptReason: "provider",
    });
    expect(document.querySelector(".agent-retry-btn")?.textContent).toBe("Retry");
  });

  it("does not keep an actionable button on an old interrupted message", () => {
    const { controller } = makeController();
    controller.conversation = {
      id: "room-a",
      messages: [
        { role: "user", content: "first" },
        { role: "assistant", content: "failed", status: "interrupted", retryOnly: true },
        { role: "user", content: "second" },
      ],
    } as never;

    controller.renderThread();

    const interrupted = document.querySelector(".agent-message-interrupted");
    expect(interrupted?.querySelector(".agent-retry-btn")).toBeNull();
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
