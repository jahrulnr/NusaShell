import { describe, expect, it } from "vitest";
import {
  decideAutoContinue,
  normalizeMaxAutoContinues,
  DEFAULT_MAX_AUTO_CONTINUES,
  MAX_AUTO_CONTINUES_CAP,
  type AgentTodoItem,
} from "../src/index.js";

const item = (id: string, status: AgentTodoItem["status"], content = id): AgentTodoItem => ({ id, content, status });

describe("auto-continue policy", () => {
  it("continues when open todos remain and the chain is under the limit", () => {
    const decision = decideAutoContinue({
      items: [item("1", "pending"), item("2", "in_progress"), item("3", "completed")],
      autoContinueIndex: 0,
      maxAutoContinues: 10,
      turnOk: true,
      hasConversation: true,
    });
    expect(decision).toMatchObject({ shouldContinue: true, openTodoCount: 2, continuesUsed: 0, reason: "continue" });
  });

  it("stops with no-open-todos when every item is completed", () => {
    const decision = decideAutoContinue({
      items: [item("1", "completed"), item("2", "completed")],
      autoContinueIndex: 2,
      maxAutoContinues: 10,
      turnOk: true,
      hasConversation: true,
    });
    expect(decision).toMatchObject({ shouldContinue: false, openTodoCount: 0, reason: "no-open-todos" });
  });

  it("stops with no-open-todos when the list is empty", () => {
    const decision = decideAutoContinue({
      items: [],
      autoContinueIndex: 0,
      maxAutoContinues: 10,
      turnOk: true,
      hasConversation: true,
    });
    expect(decision).toMatchObject({ shouldContinue: false, openTodoCount: 0, reason: "no-open-todos" });
  });

  it("stops with max-reached when the chain has exhausted the budget", () => {
    const decision = decideAutoContinue({
      items: [item("1", "pending")],
      autoContinueIndex: 10,
      maxAutoContinues: 10,
      turnOk: true,
      hasConversation: true,
    });
    expect(decision).toMatchObject({ shouldContinue: false, continuesUsed: 10, reason: "max-reached" });
  });

  it("continues past any count when maxAutoContinues is 0 (unlimited)", () => {
    const decision = decideAutoContinue({
      items: [item("1", "pending")],
      autoContinueIndex: 999,
      maxAutoContinues: 0,
      turnOk: true,
      hasConversation: true,
    });
    expect(decision).toMatchObject({ shouldContinue: true, continuesUsed: 999, maxAutoContinues: 0, reason: "continue" });
  });

  it("stops with turn-not-ok when the sealed turn failed", () => {
    const decision = decideAutoContinue({
      items: [item("1", "pending")],
      autoContinueIndex: 1,
      maxAutoContinues: 10,
      turnOk: false,
      hasConversation: true,
    });
    expect(decision).toMatchObject({ shouldContinue: false, reason: "turn-not-ok" });
  });

  it("stops with no-conversation when no conversation is bound", () => {
    const decision = decideAutoContinue({
      items: [item("1", "pending")],
      autoContinueIndex: 0,
      maxAutoContinues: 10,
      turnOk: true,
      hasConversation: false,
    });
    expect(decision).toMatchObject({ shouldContinue: false, reason: "no-conversation" });
  });

  it("counts only pending/in_progress items as open", () => {
    const decision = decideAutoContinue({
      items: [item("a", "completed"), item("b", "in_progress"), item("c", "pending"), item("d", "completed")],
      autoContinueIndex: 3,
      maxAutoContinues: 10,
      turnOk: true,
      hasConversation: true,
    });
    expect(decision.openTodoCount).toBe(2);
    expect(decision.continuesUsed).toBe(3);
  });
});

describe("normalizeMaxAutoContinues", () => {
  it("falls back to the default for non-finite values", () => {
    expect(normalizeMaxAutoContinues(undefined)).toBe(DEFAULT_MAX_AUTO_CONTINUES);
    expect(normalizeMaxAutoContinues(Number.NaN)).toBe(DEFAULT_MAX_AUTO_CONTINUES);
  });

  it("treats 0 as the unlimited sentinel (kept as 0, not default)", () => {
    expect(normalizeMaxAutoContinues(0)).toBe(0);
  });

  it("maps negatives to the default for safety", () => {
    expect(normalizeMaxAutoContinues(-5)).toBe(DEFAULT_MAX_AUTO_CONTINUES);
  });

  it("caps at the ceiling when finite", () => {
    expect(normalizeMaxAutoContinues(MAX_AUTO_CONTINUES_CAP + 50)).toBe(MAX_AUTO_CONTINUES_CAP);
  });

  it("floors fractional values", () => {
    expect(normalizeMaxAutoContinues(3.9)).toBe(3);
  });
});
