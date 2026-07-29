import { describe, expect, it } from "vitest";
import { clampModelEffort, formatContextUsage, modelCompatibility, modelVisionStatus, searchModels } from "../src/renderer/ai-model-ui.js";

const visionModel = {
  id: "openai/gpt-5",
  label: "GPT-5",
  providerName: "OpenRouter",
  inputModes: ["text", "image", "file"],
  outputModes: ["text"],
  supportedEfforts: ["minimal", "low", "medium", "high", "xhigh"],
  defaultEffort: "medium",
  supportsTools: true,
};

describe("agent model UI projections", () => {
  it("shows provider compatibility independently from effort", () => {
    expect(modelCompatibility(visionModel)).toEqual(["vision", "document", "tools", "reasoning"]);
  });

  it("reports confirmed, unavailable, and unknown vision support separately", () => {
    expect(modelVisionStatus(visionModel)).toBe("supported");
    expect(modelVisionStatus({ ...visionModel, inputModes: ["text"] })).toBe("unsupported");
    expect(modelVisionStatus({ ...visionModel, inputModes: [] })).toBe("unknown");
    expect(modelVisionStatus({ ...visionModel, inputModes: [], supportsVision: false })).toBe("unsupported");
  });

  it("searches model ID, label, and provider name", () => {
    expect(searchModels([visionModel], "openrouter")).toEqual([visionModel]);
    expect(searchModels([visionModel], "gpt-5")).toEqual([visionModel]);
    expect(searchModels([visionModel], "claude")).toEqual([]);
  });

  it("formats context usage as used of maximum", () => {
    expect(formatContextUsage(12_400, 200_000)).toBe("12K/200K context");
    expect(formatContextUsage(1_200_000, 1_000_000)).toBe("1.2M/1M context");
    expect(formatContextUsage(0, 0)).toBe("0 ctx");
  });

  it("clamps unsupported effort to the model default while preserving auto", () => {
    expect(clampModelEffort(visionModel, "auto")).toBe("auto");
    expect(clampModelEffort(visionModel, "max")).toBe("medium");
    expect(clampModelEffort(visionModel, "xhigh")).toBe("xhigh");
    expect(clampModelEffort({ ...visionModel, supportedEfforts: [] }, "high")).toBe("auto");
  });
});
