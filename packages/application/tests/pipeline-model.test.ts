import { describe, expect, it } from "vitest";
import {
  detectCycle,
  topologicalSort,
  validatePipeline,
  type PipelineStep,
} from "../src/job/pipeline-model.js";

function step(id: string, deps?: string[]): PipelineStep {
  return {
    id,
    name: `Step ${id}`,
    action: { type: "agent", prompt: "test" },
    ...(deps ? { dependsOn: deps } : {}),
  };
}

describe("detectCycle", () => {
  it("returns null for acyclic graph", () => {
    const steps = [step("a"), step("b", ["a"]), step("c", ["b"])];
    expect(detectCycle(steps)).toBeNull();
  });

  it("detects self-cycle", () => {
    const steps = [step("a", ["a"])];
    expect(detectCycle(steps)).not.toBeNull();
    expect(detectCycle(steps)).toContain("a");
  });

  it("detects two-node cycle", () => {
    const steps = [step("a", ["b"]), step("b", ["a"])];
    const cycle = detectCycle(steps);
    expect(cycle).not.toBeNull();
    expect(cycle).toContain("a");
    expect(cycle).toContain("b");
  });

  it("detects three-node cycle", () => {
    const steps = [step("a", ["c"]), step("b", ["a"]), step("c", ["b"])];
    const cycle = detectCycle(steps);
    expect(cycle).not.toBeNull();
  });

  it("returns null for diamond (no cycle)", () => {
    const steps = [step("a"), step("b", ["a"]), step("c", ["a"]), step("d", ["b", "c"])];
    expect(detectCycle(steps)).toBeNull();
  });
});

describe("topologicalSort", () => {
  it("sorts linear chain", () => {
    const steps = [step("c", ["b"]), step("a"), step("b", ["a"])];
    const sorted = topologicalSort(steps);
    const ids = sorted.map(s => s.id);
    expect(ids.indexOf("a")).toBeLessThan(ids.indexOf("b"));
    expect(ids.indexOf("b")).toBeLessThan(ids.indexOf("c"));
  });

  it("sorts diamond", () => {
    const steps = [step("d", ["b", "c"]), step("b", ["a"]), step("c", ["a"]), step("a")];
    const sorted = topologicalSort(steps);
    const ids = sorted.map(s => s.id);
    expect(ids.indexOf("a")).toBeLessThan(ids.indexOf("b"));
    expect(ids.indexOf("a")).toBeLessThan(ids.indexOf("c"));
    expect(ids.indexOf("b")).toBeLessThan(ids.indexOf("d"));
    expect(ids.indexOf("c")).toBeLessThan(ids.indexOf("d"));
  });

  it("throws on cycle", () => {
    const steps = [step("a", ["b"]), step("b", ["a"])];
    expect(() => topologicalSort(steps)).toThrow(/cycle/i);
  });
});

describe("validatePipeline", () => {
  it("returns null for valid pipeline", () => {
    const steps = [step("a"), step("b", ["a"])];
    expect(validatePipeline(steps)).toBeNull();
  });

  it("returns error for empty steps", () => {
    expect(validatePipeline([])).toMatch(/at least one step/i);
  });

  it("returns error for duplicate ids", () => {
    const steps = [step("a"), step("a")];
    expect(validatePipeline(steps)).toMatch(/duplicate/i);
  });

  it("returns error for unknown dependency", () => {
    const steps = [step("a", ["nonexistent"])];
    expect(validatePipeline(steps)).toMatch(/unknown step/i);
  });

  it("returns error for cycle", () => {
    const steps = [step("a", ["b"]), step("b", ["a"])];
    expect(validatePipeline(steps)).toMatch(/cycle/i);
  });
});
