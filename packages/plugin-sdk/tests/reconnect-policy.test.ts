import { describe, expect, it } from "vitest";
import { ReconnectPolicy, DEFAULT_RECONNECT_OPTIONS } from "../src/client/reconnect-policy.js";

describe("ReconnectPolicy", () => {
  describe("default options", () => {
    it("starts with attempt 0 and not exhausted", () => {
      const policy = new ReconnectPolicy();
      expect(policy.currentAttempt).toBe(0);
      expect(policy.isExhausted).toBe(false);
    });

    it("shouldRetry returns true when enabled and attempts remain", () => {
      const policy = new ReconnectPolicy();
      expect(policy.shouldRetry()).toBe(true);
    });

    it("shouldRetry returns false when disabled", () => {
      const policy = new ReconnectPolicy({ enabled: false });
      expect(policy.shouldRetry()).toBe(false);
    });
  });

  describe("getDelay", () => {
    it("returns initialDelayMs on first attempt (within jitter)", () => {
      const policy = new ReconnectPolicy({
        initialDelayMs: 1000,
        maxDelayMs: 30000,
        backoffFactor: 2,
        jitterMs: 0,
      });
      expect(policy.getDelay()).toBe(1000);
    });

    it("increases exponentially: 1000 → 2000 → 4000 → 8000", () => {
      const policy = new ReconnectPolicy({
        initialDelayMs: 1000,
        maxDelayMs: 30000,
        backoffFactor: 2,
        jitterMs: 0,
      });
      expect(policy.getDelay()).toBe(1000);
      policy.recordAttempt();
      expect(policy.getDelay()).toBe(2000);
      policy.recordAttempt();
      expect(policy.getDelay()).toBe(4000);
      policy.recordAttempt();
      expect(policy.getDelay()).toBe(8000);
    });

    it("caps at maxDelayMs", () => {
      const policy = new ReconnectPolicy({
        initialDelayMs: 1000,
        maxDelayMs: 5000,
        backoffFactor: 2,
        jitterMs: 0,
      });
      policy.recordAttempt();
      policy.recordAttempt();
      policy.recordAttempt();
      expect(policy.getDelay()).toBe(5000);
    });

    it("jitter adds randomness within bounds", () => {
      const policy = new ReconnectPolicy({
        initialDelayMs: 1000,
        maxDelayMs: 30000,
        backoffFactor: 2,
        jitterMs: 500,
      });
      const delay = policy.getDelay();
      expect(delay).toBeGreaterThanOrEqual(500);
      expect(delay).toBeLessThanOrEqual(1500);
    });
  });

  describe("maxAttempts", () => {
    it("shouldRetry returns false after maxAttempts", () => {
      const policy = new ReconnectPolicy({ maxAttempts: 3 });
      expect(policy.shouldRetry()).toBe(true);
      policy.recordAttempt();
      expect(policy.shouldRetry()).toBe(true);
      policy.recordAttempt();
      expect(policy.shouldRetry()).toBe(true);
      policy.recordAttempt();
      expect(policy.shouldRetry()).toBe(false);
      expect(policy.isExhausted).toBe(true);
    });
  });

  describe("reset", () => {
    it("resets attempt count and exhausted flag", () => {
      const policy = new ReconnectPolicy({ maxAttempts: 2 });
      policy.recordAttempt();
      policy.recordAttempt();
      expect(policy.isExhausted).toBe(true);
      expect(policy.shouldRetry()).toBe(false);

      policy.reset();
      expect(policy.currentAttempt).toBe(0);
      expect(policy.isExhausted).toBe(false);
      expect(policy.shouldRetry()).toBe(true);
    });
  });

  describe("state", () => {
    it("returns current attempt and exhausted status", () => {
      const policy = new ReconnectPolicy({ maxAttempts: 2 });
      expect(policy.state).toEqual({ attempt: 0, exhausted: false });
      policy.recordAttempt();
      expect(policy.state).toEqual({ attempt: 1, exhausted: false });
      policy.recordAttempt();
      expect(policy.state).toEqual({ attempt: 2, exhausted: true });
    });
  });

  describe("DEFAULT_RECONNECT_OPTIONS", () => {
    it("has sensible defaults", () => {
      expect(DEFAULT_RECONNECT_OPTIONS.enabled).toBe(true);
      expect(DEFAULT_RECONNECT_OPTIONS.initialDelayMs).toBe(1000);
      expect(DEFAULT_RECONNECT_OPTIONS.maxDelayMs).toBe(30000);
      expect(DEFAULT_RECONNECT_OPTIONS.backoffFactor).toBe(2);
      expect(DEFAULT_RECONNECT_OPTIONS.jitterMs).toBe(500);
    });
  });
});
