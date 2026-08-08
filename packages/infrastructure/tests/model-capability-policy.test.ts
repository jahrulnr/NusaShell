import { describe, expect, it } from "vitest";
import {
  heuristicModelSupportsEffort,
  heuristicModelSupportsVision,
  resolveModelRuntimePolicy,
} from "../src/index.js";

describe("model capability policy", () => {
  it("omits auto effort even when the catalog advertises a default", () => {
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
      contextWindow: 400_000,
      maxOutput: 32_000,
      supportsTools: true,
      supportsVision: true,
    });
    expect(resolveModelRuntimePolicy({
      model: "openai/gpt-5",
      requestedEffort: "auto",
      capabilities: {
        supportedEfforts: ["low", "medium", "high"],
        defaultEffort: "medium",
        reasoningSupported: true,
      },
    }).effort).toBeUndefined();
  });

  it("sends an explicit picker effort that is in the catalog allow-list", () => {
    expect(resolveModelRuntimePolicy({
      model: "openai/gpt-5",
      requestedEffort: "medium",
      capabilities: {
        supportedEfforts: ["low", "medium", "high"],
        defaultEffort: "medium",
        reasoningSupported: true,
      },
    }).effort).toBe("medium");
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

  it("omits reasoning_effort when catalog has no effort levels (auto compatibility)", () => {
    expect(resolveModelRuntimePolicy({
      model: "gpt-4o-mini",
      requestedEffort: "high",
      capabilities: { reasoningSupported: false },
    }).effort).toBeUndefined();
    // Auto without a catalog allow-list never invents a level (tokenrouter 400ed
    // on the old medium default). Leave reasoning_effort off the wire.
    expect(resolveModelRuntimePolicy({
      model: "deepseek-r1",
      requestedEffort: "auto",
    }).effort).toBeUndefined();
    expect(resolveModelRuntimePolicy({
      model: "z-ai/glm-5.2",
      requestedEffort: "auto",
      capabilities: { reasoningSupported: false, supportedEfforts: [], defaultEffort: "auto" },
    }).effort).toBeUndefined();
    expect(resolveModelRuntimePolicy({
      model: "blackbox/kimi-k3",
      requestedEffort: "auto",
      capabilities: { reasoningSupported: false },
    }).effort).toBeUndefined();
    // Explicit pick also omitted when the catalog lists no levels.
    expect(resolveModelRuntimePolicy({
      model: "reasoner",
      requestedEffort: "medium",
      capabilities: { reasoningSupported: true, supportedEfforts: [], defaultEffort: "auto" },
    }).effort).toBeUndefined();
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
    expect(heuristicModelSupportsEffort("z-ai::glm-5.2")).toBe(true);
    expect(heuristicModelSupportsEffort("kimi-k3")).toBe(true);
    expect(heuristicModelSupportsVision("google/gemini-2.5-pro")).toBe(true);
    expect(heuristicModelSupportsVision("deepseek/deepseek-chat")).toBe(false);
  });

  it("honors explicit vision capability ahead of empty input modality metadata", () => {
    expect(resolveModelRuntimePolicy({
      model: "provider/model",
      capabilities: { inputModes: [], supportsVision: false },
    }).supportsVision).toBe(false);
  });
});
