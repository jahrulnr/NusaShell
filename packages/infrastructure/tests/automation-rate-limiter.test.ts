import { describe, expect, it } from "vitest";
import { AutomationRateLimiter, DEFAULT_AUTOMATION_RATE_LIMITS } from "../src/mcp/automation-rate-limiter.js";

describe("AutomationRateLimiter", () => {
  it("allows up to burst capacity from cold", () => {
    let now = 0;
    const limiter = new AutomationRateLimiter(
      { steadyRatePerMinute: 10, burstCapacity: 5, maxPayloadBytes: 1024 },
      () => now,
    );
    for (let i = 0; i < 5; i++) {
      expect(limiter.allow("plugin.a")).toBe(true);
    }
    expect(limiter.allow("plugin.a")).toBe(false);
  });

  it("refills tokens over time at the steady rate", () => {
    let now = 0;
    const limiter = new AutomationRateLimiter(
      { steadyRatePerMinute: 60, burstCapacity: 6, maxPayloadBytes: 1024 },
      () => now,
    );
    // Drain the bucket
    for (let i = 0; i < 6; i++) limiter.allow("plugin.a");
    expect(limiter.allow("plugin.a")).toBe(false);
    // Advance 1 second → 1 token refilled (60/min = 1/sec)
    now += 1000;
    expect(limiter.allow("plugin.a")).toBe(true);
    expect(limiter.allow("plugin.a")).toBe(false);
  });

  it("tracks per-plugin buckets independently", () => {
    let now = 0;
    const limiter = new AutomationRateLimiter(
      { steadyRatePerMinute: 10, burstCapacity: 2, maxPayloadBytes: 1024 },
      () => now,
    );
    expect(limiter.allow("plugin.a")).toBe(true);
    expect(limiter.allow("plugin.a")).toBe(true);
    expect(limiter.allow("plugin.a")).toBe(false);
    // plugin.b has its own bucket
    expect(limiter.allow("plugin.b")).toBe(true);
  });

  it("bounds payload to the configured byte cap", () => {
    const limiter = new AutomationRateLimiter({
      steadyRatePerMinute: 10,
      burstCapacity: 5,
      maxPayloadBytes: 100,
    });
    const small = limiter.boundPayload({ a: "short" });
    expect(small.truncated).toBe(false);

    const large = limiter.boundPayload({ a: "x".repeat(200) });
    expect(large.truncated).toBe(true);
    expect(large.text.length).toBe(100);
  });

  it("does not truncate payloads under the cap", () => {
    const limiter = new AutomationRateLimiter(DEFAULT_AUTOMATION_RATE_LIMITS);
    const result = limiter.boundPayload({ messageId: "abc123" });
    expect(result.truncated).toBe(false);
  });

  it("handles non-serializable payloads gracefully", () => {
    const limiter = new AutomationRateLimiter({
      steadyRatePerMinute: 10,
      burstCapacity: 5,
      maxPayloadBytes: 1024,
    });
    const circular: Record<string, unknown> = {};
    circular.self = circular;
    const result = limiter.boundPayload(circular);
    expect(result.truncated).toBe(false);
    expect(result.text.length).toBeGreaterThan(0);
  });

  it("reset clears a plugin's bucket", () => {
    let now = 0;
    const limiter = new AutomationRateLimiter(
      { steadyRatePerMinute: 10, burstCapacity: 2, maxPayloadBytes: 1024 },
      () => now,
    );
    limiter.allow("plugin.a");
    limiter.allow("plugin.a");
    expect(limiter.allow("plugin.a")).toBe(false);
    limiter.reset("plugin.a");
    expect(limiter.allow("plugin.a")).toBe(true);
  });

  it("caps refill at burst capacity", () => {
    let now = 0;
    const limiter = new AutomationRateLimiter(
      { steadyRatePerMinute: 10, burstCapacity: 3, maxPayloadBytes: 1024 },
      () => now,
    );
    // Drain partially
    limiter.allow("plugin.a");
    // Advance a very long time — should cap at burstCapacity, not exceed
    now += 60_000 * 100;
    let count = 0;
    while (limiter.allow("plugin.a")) count++;
    expect(count).toBe(3);
  });
});
