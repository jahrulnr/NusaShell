import { describe, expect, it } from "vitest";
import {
  boundContextValue,
  boundPipelineText,
  serializePipelineValue,
  DEFAULT_PIPELINE_SUMMARY_MAX_CHARS,
} from "../src/job/pipeline-output.js";

describe("pipeline output bounds", () => {
  it("serializes objects as JSON", () => {
    expect(serializePipelineValue({ a: 1 })).toBe('{"a":1}');
  });

  it("does not truncate short text", () => {
    expect(boundPipelineText("hello", 10)).toEqual({ text: "hello", truncated: false });
  });

  it("truncates long text with sentinel", () => {
    const long = "x".repeat(100);
    const result = boundPipelineText(long, 20);
    expect(result.truncated).toBe(true);
    expect(result.text.length).toBeLessThanOrEqual(20);
    expect(result.text.endsWith("…")).toBe(true);
  });

  it("uses default summary max that fits desktop retention", () => {
    expect(DEFAULT_PIPELINE_SUMMARY_MAX_CHARS).toBeGreaterThan(1000);
  });

  it("bounds context values for template chaining", () => {
    const huge = "y".repeat(10_000);
    const ctx = boundContextValue(huge, "summary", 100);
    expect(ctx.length).toBeLessThanOrEqual(100);
  });
});
