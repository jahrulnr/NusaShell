import { describe, expect, it } from "vitest";
import {
  evaluateCondition,
  evaluateConditionNode,
} from "../src/job/services/event-job-matcher.js";
import { createAutomationEvent } from "../src/events/automation-event.js";
import type { Condition, ConditionNode } from "../src/job/job-model.js";

describe("evaluateCondition — Phase D ops", () => {
  const event = createAutomationEvent("test.event", "test.plugin", {
    status: "ok",
    count: 3,
    label: "important-update",
  });

  describe("ne (not-equal)", () => {
    it("matches when value differs", () => {
      const cond: Condition = { path: "payload.status", op: "ne", value: "error" };
      expect(evaluateCondition(cond, event)).toBe(true);
    });

    it("does not match when value equals", () => {
      const cond: Condition = { path: "payload.status", op: "ne", value: "ok" };
      expect(evaluateCondition(cond, event)).toBe(false);
    });

    it("does not match when path is missing (missing = no-match)", () => {
      const cond: Condition = { path: "payload.missing", op: "ne", value: "ok" };
      expect(evaluateCondition(cond, event)).toBe(false);
    });
  });
});

describe("evaluateConditionNode — OR/NOT/nested", () => {
  const event = createAutomationEvent("test.event", "test.plugin", {
    status: "ok",
    priority: "high",
    label: "important-update",
  });

  it("OR: matches when any child matches", () => {
    const node: ConditionNode = {
      op: "or",
      any: [
        { path: "payload.status", op: "eq", value: "error" },
        { path: "payload.priority", op: "eq", value: "high" },
      ],
    };
    expect(evaluateConditionNode(node, event)).toBe(true);
  });

  it("OR: does not match when no child matches", () => {
    const node: ConditionNode = {
      op: "or",
      any: [
        { path: "payload.status", op: "eq", value: "error" },
        { path: "payload.priority", op: "eq", value: "low" },
      ],
    };
    expect(evaluateConditionNode(node, event)).toBe(false);
  });

  it("NOT: inverts a matching condition", () => {
    const node: ConditionNode = {
      op: "not",
      of: { path: "payload.status", op: "eq", value: "error" },
    };
    expect(evaluateConditionNode(node, event)).toBe(true);
  });

  it("NOT: inverts a non-matching condition", () => {
    const node: ConditionNode = {
      op: "not",
      of: { path: "payload.status", op: "eq", value: "ok" },
    };
    expect(evaluateConditionNode(node, event)).toBe(false);
  });

  it("nested: NOT(OR(...))", () => {
    const node: ConditionNode = {
      op: "not",
      of: {
        op: "or",
        any: [
          { path: "payload.status", op: "eq", value: "error" },
          { path: "payload.priority", op: "eq", value: "low" },
        ],
      },
    };
    expect(evaluateConditionNode(node, event)).toBe(true);
  });

  it("nested: OR with NOT child", () => {
    const node: ConditionNode = {
      op: "or",
      any: [
        { path: "payload.status", op: "eq", value: "error" },
        { op: "not", of: { path: "payload.priority", op: "eq", value: "low" } },
      ],
    };
    expect(evaluateConditionNode(node, event)).toBe(true);
  });

  it("leaf condition passes through evaluateConditionNode", () => {
    const node: ConditionNode = { path: "payload.status", op: "eq", value: "ok" };
    expect(evaluateConditionNode(node, event)).toBe(true);
  });
});
