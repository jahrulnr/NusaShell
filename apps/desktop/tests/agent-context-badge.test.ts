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

function makeController(getActiveModel: () => any, getMaxInputTokens: () => number | undefined) {
  const controller = new AgentConversationController({
    getActiveModel,
    getMaxInputTokens,
  } as never);
  controller.conversation = {
    id: "c1",
    messages: [{ role: "user", content: "hi" }],
    checkpoint: null,
  } as never;
  controller.activeId = "c1";
  return controller;
}

describe("AgentConversationController — context badge consistency (tickets #40/#41)", () => {
  beforeEach(() => installDom());

  it("idle badge uses effective window = min(global cap, model window), not raw model window", () => {
    // #41: backend resolveContextThreshold uses min(settings.maxInputTokens,
    // modelWindow). The idle badge must match, so a 1M model bounded by a
    // 200k global cap shows 200k — not 1M.
    const controller = makeController(
      () => ({ key: "m1M", contextWindow: 1_000_000 }),
      () => 200_000,
    );
    const status = document.getElementById("agent-provider-status")!;
    controller.updateContextStatus();
    expect(status.textContent).toContain("/200k context");
    expect(status.textContent).not.toContain("/1M context");
  });

  it("idle badge keeps the full model window when the global cap is larger or absent", () => {
    const controller = makeController(
      () => ({ key: "m1M", contextWindow: 1_000_000 }),
      () => 1_500_000,
    );
    const status = document.getElementById("agent-provider-status")!;
    controller.updateContextStatus();
    expect(status.textContent).toContain("/1M context");

    // No global cap configured → fall through to the model window.
    const controller2 = makeController(
      () => ({ key: "m1M", contextWindow: 1_000_000 }),
      () => undefined,
    );
    const status2 = document.getElementById("agent-provider-status")!;
    // Reset cache between controllers.
    controller2._lastContextKey = "";
    controller2._lastContextText = null;
    controller2.updateContextStatus();
    expect(status2.textContent).toContain("/1M context");
  });

  it("invalidate the badge cache when the model changes, even with identical messages", () => {
    // #40: _lastContextKey previously omitted the model key, so switching
    // models without changing the thread kept showing stale text with the
    // wrong denominator.
    const controller = makeController(
      () => ({ key: "m1M", contextWindow: 1_000_000 }),
      () => 200_000,
    );
    const status = document.getElementById("agent-provider-status")!;
    controller.updateContextStatus();
    const first = status.textContent;

    controller.getActiveModel = () => ({ key: "mSmall", contextWindow: 2_000 });
    controller.updateContextStatus();
    expect(status.textContent).not.toBe(first);
    expect(status.textContent).toContain("/2k context");
  });

  it("live badge uses the snapshot turn model, not the global picker that changed mid-turn", () => {
    // #40 root cause 1: updateContextStatus re-read getActiveModel() (global).
    // While a turn streams, it must use the model bound to the turn (1M → 200k
    // effective) even if the global picker was switched to a 2k model.
    const controller = makeController(
      () => ({ key: "mSmall", contextWindow: 2_000 }),
      () => 200_000,
    );
    controller.liveStreamState = { modelKey: "m1M", contextWindow: 1_000_000 } as never;
    const status = document.getElementById("agent-provider-status")!;
    controller.updateContextStatus();
    expect(status.textContent).toContain("/200k context");
    expect(status.textContent).not.toContain("/2k context");
  });

  it("post-turn idle badge re-renders after the stream state is cleared", () => {
    const controller = makeController(
      () => ({ key: "m1M", contextWindow: 1_000_000 }),
      () => 200_000,
    );
    const status = document.getElementById("agent-provider-status")!;
    // Stream phase: snapshot model (1M) → effective 200k.
    controller.liveStreamState = { modelKey: "m1M", contextWindow: 1_000_000 } as never;
    controller.updateContextStatus();
    const duringStream = status.textContent;

    // Stream cleared (idle): global model is now a 128k window → must re-render.
    controller.liveStreamState = null as never;
    controller.getActiveModel = () => ({ key: "m128", contextWindow: 128_000 });
    controller.updateContextStatus();
    expect(status.textContent).not.toBe(duringStream);
    expect(status.textContent).toContain("/128k context");
  });
});
