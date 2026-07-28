import { describe, expect, it } from "vitest";
import {
  heuristicModelSupportsEffort,
  heuristicModelSupportsVision,
  resolveModelRuntimePolicy,
} from "../src/index.js";

describe("model capability policy", () => {
  it("resolves auto effort from advertised catalog metadata", () => {
    expect(resolveModelRuntimePolicy({
      model: "openai/gpt-5",
      requestedEffort: "auto",
      capabilities: {
        contextWindow: 400_000,
        maxOutput: 32_000,
        inputModes: ["text", "image"],
        outputModes: ["text"],
        supportedEfforts: ["low", "medium", "high"],
        defaultEffort: "medium",
        reasoningSupported: true,
        reasoningMandatory: false,
        supportsTools: true,
      },
    })).toMatchObject({
      effort: "medium",
      contextWindow: 400_000,
      maxOutput: 32_000,
      supportsTools: true,
      supportsVision: true,
    });
  });

  it("clamps explicit effort to the nearest advertised level", () => {
    expect(resolveModelRuntimePolicy({
      model: "reasoner",
      requestedEffort: "xhigh",
      capabilities: {
        supportedEfforts: ["low", "medium", "high"],
        defaultEffort: "medium",
        reasoningSupported: true,
      },
    }).effort).toBe("high");
  });

  it("never sends none to a mandatory reasoning model", () => {
    expect(resolveModelRuntimePolicy({
      model: "reasoner",
      requestedEffort: "none",
      capabilities: {
        supportedEfforts: ["none", "low", "high"],
        defaultEffort: "low",
        reasoningSupported: true,
        reasoningMandatory: true,
      },
    }).effort).toBe("low");
  });

  it("omits unsupported effort and uses conservative heuristics only when metadata is absent", () => {
    expect(resolveModelRuntimePolicy({
      model: "gpt-4o-mini",
      requestedEffort: "high",
      capabilities: { reasoningSupported: false },
    }).effort).toBeUndefined();
    expect(resolveModelRuntimePolicy({
      model: "deepseek-r1",
      requestedEffort: "auto",
    }).effort).toBe("medium");
  });

  it("keeps tools enabled when catalog support is unknown and disables only explicit false", () => {
    expect(resolveModelRuntimePolicy({
      model: "provider/model",
      capabilities: {},
    }).supportsTools).toBe(true);
    expect(resolveModelRuntimePolicy({
      model: "provider/model",
      capabilities: { supportsTools: false },
    }).supportsTools).toBe(false);
  });

  it("recognizes known reasoning and vision model families conservatively", () => {
    expect(heuristicModelSupportsEffort("anthropic/claude-sonnet-4")).toBe(true);
    expect(heuristicModelSupportsEffort("openai/gpt-4o-mini")).toBe(false);
    expect(heuristicModelSupportsVision("google/gemini-2.5-pro")).toBe(true);
    expect(heuristicModelSupportsVision("deepseek/deepseek-chat")).toBe(false);
  });
});
