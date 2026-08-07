// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { bindLazyCanvasReveal } from "../src/renderer/agent-canvas-lazy.js";

function createObserverStub() {
  const callbacks = [];
  const instances = [];
  const stub = {
    instances,
    callbacks,
    observe: vi.fn(),
    unobserve: vi.fn(),
    disconnect: vi.fn(),
    trigger(entries) {
      for (const cb of callbacks) cb(entries, stub);
    },
  };
  stub.Instance = class {
    constructor(cb) {
      callbacks.push(cb);
      instances.push(this);
      this.observe = stub.observe;
      this.unobserve = stub.unobserve;
      this.disconnect = stub.disconnect;
    }
  };
  return stub;
}

/** Install a working IntersectionObserver stub for this test. */
function withIo() {
  const holder = { io: null };
  const stub = createObserverStub();
  holder.io = stub;
  vi.stubGlobal("IntersectionObserver", stub.Instance);
  return holder;
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("agent canvas lazy reveal", () => {
  it("renders a placeholder and defers onReveal until the element intersects", () => {
    const holder = withIo();
    const host = document.createElement("div");
    const onReveal = vi.fn(() => {
      const container = document.createElement("div");
      container.className = "agent-canvas-inline";
      container.textContent = "rendered";
      host.appendChild(container);
      return Promise.resolve();
    });
    const getContainer = () => host.querySelector(".agent-canvas-inline") ?? null;

    bindLazyCanvasReveal({ host, getContainer, onReveal, root: document.body });

    expect(host.querySelector(".agent-canvas-lazy-placeholder")).not.toBeNull();
    expect(onReveal).not.toHaveBeenCalled();
    expect(getContainer()).toBeNull();

    holder.io.trigger([{ isIntersecting: false }]);
    expect(onReveal).not.toHaveBeenCalled();

    holder.io.trigger([{ isIntersecting: true }]);
    expect(onReveal).toHaveBeenCalledTimes(1);
    expect(getContainer()).not.toBeNull();
    expect(host.querySelector(".agent-canvas-lazy-placeholder")).toBeNull();
  });

  it("reveals via the Load preview button even before intersection", () => {
    withIo();
    const host = document.createElement("div");
    const onReveal = vi.fn(() => Promise.resolve());
    bindLazyCanvasReveal({ host, getContainer: () => null, onReveal, root: document.body });
    const btn = host.querySelector(".agent-canvas-lazy-placeholder .agent-canvas-fence-btn.is-primary");
    expect(btn).not.toBeNull();
    btn.click();
    expect(onReveal).toHaveBeenCalledTimes(1);
    expect(host.querySelector(".agent-canvas-lazy-placeholder")).toBeNull();
  });

  it("dispose removes the placeholder and stops observing without rendering", () => {
    withIo();
    const host = document.createElement("div");
    const onReveal = vi.fn();
    const controller = bindLazyCanvasReveal({ host, getContainer: () => null, onReveal, root: document.body });
    controller.dispose();
    expect(host.querySelector(".agent-canvas-lazy-placeholder")).toBeNull();
    expect(onReveal).not.toHaveBeenCalled();
  });

  it("fallback renders immediately when IntersectionObserver is unavailable", () => {
    vi.stubGlobal("IntersectionObserver", undefined);
    const host = document.createElement("div");
    const onReveal = vi.fn();
    bindLazyCanvasReveal({ host, getContainer: () => null, onReveal, root: document.body });
    expect(onReveal).toHaveBeenCalledTimes(1);
  });
});
