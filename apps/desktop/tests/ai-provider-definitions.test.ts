import { describe, expect, it } from "vitest";
import {
  inferProviderType,
  normalizeProviderInput,
  providerDefinition,
} from "../src/main/ai-provider-definitions.js";

describe("AI provider definitions", () => {
  it("infers legacy connections by host before falling back to custom", () => {
    expect(inferProviderType({ id: "legacy", baseUrl: "https://openrouter.ai/api/v1" })).toBe("openrouter");
    expect(inferProviderType({ id: "legacy", baseUrl: "http://127.0.0.1:20128/v1" })).toBe("omniroute");
    expect(inferProviderType({ id: "legacy", baseUrl: "https://private.example/v1" })).toBe("openai-compatible");
  });
  it("applies provider defaults without overwriting explicit connection values", () => {
    const normalized = normalizeProviderInput({
      id: "openai",
      name: "",
      type: "openai",
      api: "responses",
      baseUrl: "",
      apiKeyOptional: true,
      enabled: true,
    });
    expect(normalized).toMatchObject({
      name: "OpenAI",
      type: "openai",
      api: "responses",
      baseUrl: "https://api.openai.com/v1",
      apiKeyOptional: false,
    });
    expect(providerDefinition("openai-compatible").hideFromCatalog).toBe(true);
  });
});
