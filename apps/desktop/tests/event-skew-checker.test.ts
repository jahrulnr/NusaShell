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

import { checkEventSkew } from "../src/shared/event-skew-checker.js";

describe("checkEventSkew", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(1000);
  });

  it("returns no warning when skew is below threshold", () => {
    const warn = vi.fn();
    const result = checkEventSkew(
      { event: "agent.text_delta", sequence: 1, emittedAt: 900 },
      { now: 1000, thresholdMs: 250, warn, lastWarnAt: 0 },
    );
    expect(result.warned).toBe(false);
    expect(warn).not.toHaveBeenCalled();
  });

  it("warns when skew exceeds threshold", () => {
    const warn = vi.fn();
    const result = checkEventSkew(
      { event: "agent.text_delta", sequence: 5, emittedAt: 500 },
      { now: 1000, thresholdMs: 250, warn, lastWarnAt: 0 },
    );
    expect(result.warned).toBe(true);
    expect(warn).toHaveBeenCalledTimes(1);
    const msg = warn.mock.calls[0][0];
    expect(msg).toContain("ipc.skew");
    expect(msg).toContain("event=agent.text_delta");
    expect(msg).toContain("skewMs=500");
    expect(msg).toContain("sequence=5");
  });

  it("does not warn again within flood window", () => {
    const warn = vi.fn();
    const ctx = { now: 1000, thresholdMs: 250, warn, lastWarnAt: 0 };
    const r1 = checkEventSkew({ event: "x", sequence: 1, emittedAt: 500 }, ctx);
    expect(r1.warned).toBe(true);
    ctx.lastWarnAt = r1.lastWarnAt;
    ctx.now = 1100;
    const r2 = checkEventSkew({ event: "x", sequence: 2, emittedAt: 600 }, ctx);
    expect(r2.warned).toBe(false);
  });

  it("warns again after flood window expires", () => {
    const warn = vi.fn();
    const ctx = { now: 1000, thresholdMs: 250, warn, lastWarnAt: 0 };
    const r1 = checkEventSkew({ event: "x", sequence: 1, emittedAt: 500 }, ctx);
    ctx.lastWarnAt = r1.lastWarnAt;
    ctx.now = 6000;
    const r2 = checkEventSkew({ event: "x", sequence: 2, emittedAt: 5600 }, ctx);
    expect(r2.warned).toBe(true);
  });

  it("no-ops when emittedAt is missing", () => {
    const warn = vi.fn();
    const result = checkEventSkew(
      { event: "x", sequence: 1 } as never,
      { now: 1000, thresholdMs: 250, warn, lastWarnAt: 0 },
    );
    expect(result.warned).toBe(false);
    expect(warn).not.toHaveBeenCalled();
  });
});
