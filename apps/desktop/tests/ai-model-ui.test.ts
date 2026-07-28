import { describe, expect, it } from "vitest";
import { clampModelEffort, modelCompatibility, searchModels } from "../src/renderer/ai-model-ui.js";

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

  it("searches model ID, label, and provider name", () => {
    expect(searchModels([visionModel], "openrouter")).toEqual([visionModel]);
    expect(searchModels([visionModel], "gpt-5")).toEqual([visionModel]);
    expect(searchModels([visionModel], "claude")).toEqual([]);
  });

  it("clamps unsupported effort to the model default while preserving auto", () => {
    expect(clampModelEffort(visionModel, "auto")).toBe("auto");
    expect(clampModelEffort(visionModel, "max")).toBe("medium");
    expect(clampModelEffort(visionModel, "xhigh")).toBe("xhigh");
    expect(clampModelEffort({ ...visionModel, supportedEfforts: [] }, "high")).toBe("auto");
  });
});
