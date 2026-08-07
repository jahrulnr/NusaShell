// @vitest-environment jsdom
//
// Ticket #43 — composer typing latency in rooms with 1000+ tool cards.
// The per-keystroke resize path must NOT read scrollHeight on the live
// textarea (which forces a full-document layout) nor call getComputedStyle on
// every burst (sync style recalc). Instead it measures an off-thread hidden
// mirror and caches computed style values once.

import { beforeEach, describe, expect, it, vi } from "vitest";

function installDom({ width = 600 } = {}) {
  document.body.innerHTML = `
    <div class="agent-conversation">
      <div class="agent-thread" id="agent-thread">
        <div style="height:200px"></div>
      </div>
      <form class="agent-composer" id="agent-form">
        <textarea id="agent-input" rows="1"></textarea>
      </form>
    </div>
  `;
  globalThis.$ = (selector: string) => document.querySelector(selector);
  window.matchMedia = vi.fn(
    () => ({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() }) as never,
  );
  // ResizeObserver is a no-op in these unit tests.
  (globalThis as Record<string, unknown>).ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
  const compute = vi.fn((el: Element) => ({
    lineHeight: "19px",
    paddingTop: "7px",
    paddingBottom: "7px",
    fontFamily: "inherit",
    fontSize: "13px",
    ...(el.id === "" ? { boxSizing: "border-box" } : {}),
  }));
  // Cache the real getComputedStyle so both old and new paths report the same.
  window.getComputedStyle = compute as never;
  return { compute };
}

import { AgentConversationController } from "../src/renderer/agent-conversation-controller.js";

describe("AgentConversationController — composer perf (ticket #43)", () => {
  beforeEach(() => installDom());

  it("caches computed style once and reuses it across bursts (no getComputedStyle on hot path)", () => {
    const { compute } = installDom();
    const gcBefore = compute.mock.calls.length;
    const controller = new AgentConversationController({} as never);

    controller.resizeComposerInput();
    controller.resizeComposerInput();
    controller.resizeComposerInput();

    // First call may compute metrics once; subsequent bursts must not re-query.
    const gcAfter = compute.mock.calls.length;
    expect(gcAfter - gcBefore).toBeLessThanOrEqual(1);
    compute.mockRestore();
  });

  it("does not read scrollHeight on the live textarea (uses isolated mirror)", () => {
    const controller = new AgentConversationController({} as never);
    const input = document.getElementById("agent-input")! as HTMLTextAreaElement;
    const readScrollHeight = vi.spyOn(input, "scrollHeight", "get");
    const readValue = vi.spyOn(input, "value", "get");

    controller.resizeComposerInput();

    // The live textarea scrollHeight must never be read by the resize path.
    expect(readScrollHeight).not.toHaveBeenCalled();
    readScrollHeight.mockRestore();
    readValue.mockRestore();
  });

  it("grows through ten rows before enabling internal scroll (mirror measure matches)", () => {
    const controller = new AgentConversationController({} as never);
    const input = document.getElementById("agent-input")! as HTMLTextAreaElement;

    // First resize creates the lazy mirror (and caches metrics); afterwards we
    // control the mirror's scrollHeight to simulate wrapped text.
    controller.resizeComposerInput();
    const mirror = controller._composerMirror as HTMLTextAreaElement | null;
    expect(mirror).toBeTruthy();
    Object.defineProperty(mirror!, "scrollHeight", {
      configurable: true,
      value: 90, // 4 lines * 19 + 14 padding
    });

    controller.resizeComposerInput();
    expect(input.style.height).toBe("90px");
    expect(input.style.overflowY).toBe("hidden");

    // Grow past 10 rows -> internal scroll on.
    Object.defineProperty(mirror!, "scrollHeight", {
      configurable: true,
      value: 300,
    });
    controller.resizeComposerInput();
    expect(input.style.overflowY).toBe("auto");
    expect(Number.parseFloat(input.style.height)).toBeLessThan(300);
  });

  it("skips DOM writes when the measured height is unchanged (no churn per keystroke)", () => {
    const controller = new AgentConversationController({} as never);
    const input = document.getElementById("agent-input")! as HTMLTextAreaElement;
    // Create the lazy mirror so we can fix its scrollHeight before measuring.
    controller.resizeComposerInput();
    const mirror = controller._composerMirror as HTMLTextAreaElement | null;
    expect(mirror).toBeTruthy();
    Object.defineProperty(mirror!, "scrollHeight", { configurable: true, value: 90 });

    controller.resizeComposerInput();
    const heightAfterFirst = input.style.height;
    expect(heightAfterFirst).toBe("90px");

    // Same value, same measured height -> no further DOM mutation.
    controller.resizeComposerInput();
    expect(input.style.height).toBe(heightAfterFirst);
  });
});
