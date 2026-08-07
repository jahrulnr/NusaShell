// @vitest-environment jsdom
/**
 * Ticket #47 — Ctrl+Enter races a stale store snapshot against this.conversation.
 *
 * While turn N's assistant reply is still being sealed by main, submitting
 * turn N+1 calls store.append(user) and may get a conversation snapshot that
 * has not yet included turn N's assistant message. The renderer must not let
 * that stale snapshot replace the in-memory messages and make the assistant
 * reply "disappear" from the thread.
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

describe("AgentConversationController — stale-snapshot race (ticket #47)", () => {
  beforeEach(() => installDom());

  it("keeps an already-seen assistant message when a later append returns a stale snapshot", async () => {
    // Turn 2's assistant message is already in memory (thread shows it).
    const seenAssistant = {
      role: "assistant",
      content: "answer 2",
      traceId: "tr-2",
      createdAt: "t-4",
    };
    const base = [
      { role: "user", content: "1", createdAt: "t1" },
      { role: "assistant", content: "a1", traceId: "tr-1", createdAt: "t2" },
      { role: "user", content: "2", createdAt: "t3" },
    ];
    const controller = new AgentConversationController({
      shell: {
        agentConversations: {
          append: vi.fn(async (_id: string, msg: unknown) => ({
            id: "c1",
            kind: "agent",
            // append(user3) returns a snapshot that does NOT yet include
            // assistant 2 (its seal hasn't committed).
            messages: [...base, { role: "user", content: "3", createdAt: "t5" }],
          })),
          get: vi.fn(async () => ({ id: "c1", kind: "agent", messages: base })),
          list: vi.fn(async () => []),
        },
      },
      runTurn: vi.fn(async () => ({ traceId: "tr-3", text: "a3", toolCalls: [], rounds: 1 })),
      getActiveModel: () => null,
      log: vi.fn(),
    } as never);
    // Seed in-memory state WITH assistant 2 visible (the renderer already showed it).
    controller.conversation = { id: "c1", kind: "agent", messages: [...base, seenAssistant] } as never;
    controller.activeId = "c1";
    const input = document.querySelector<HTMLInputElement>("#agent-input")!;
    input.value = "hello";
    controller.refresh = vi.fn(async () => {});

    await controller.submit();

    // After the stale append of user 3, the in-memory conversation must STILL
    // contain assistant 2 (no disappearance from the thread).
    const contents = (controller.conversation!.messages as { role: string; content: string }[])
      .map((m) => `${m.role}:${m.content}`);
    expect(contents).toContain("user:3");
    expect(contents).toContain("assistant:answer 2");
  });
});
