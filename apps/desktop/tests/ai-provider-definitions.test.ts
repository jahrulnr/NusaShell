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
  it("infers ollama by id or host", () => {
    expect(inferProviderType({ id: "ollama", baseUrl: "" })).toBe("ollama");
    expect(inferProviderType({ id: "legacy", baseUrl: "http://127.0.0.1:11434/v1" })).toBe("ollama");
  });
  it("infers llamacpp by id or localhost host marker", () => {
    expect(inferProviderType({ id: "llamacpp", baseUrl: "" })).toBe("llamacpp");
    expect(inferProviderType({ id: "legacy", baseUrl: "http://localhost:8080/v1" })).toBe("llamacpp");
    expect(inferProviderType({ id: "legacy", baseUrl: "http://127.0.0.1:8080/v1" })).toBe("llamacpp");
  });
  it("does not infer llamacpp for a non-localhost 8080 URL", () => {
    expect(inferProviderType({ id: "legacy", baseUrl: "http://10.0.0.5:8080/v1" })).toBe("openai-compatible");
    expect(inferProviderType({ id: "legacy", baseUrl: "https://api.example:8080/v1" })).toBe("openai-compatible");
  });
  it("applies ollama and llamacpp preset defaults", () => {
    expect(providerDefinition("ollama")).toMatchObject({
      api: "chat",
      baseUrl: "http://127.0.0.1:11434/v1",
      apiKeyOptional: true,
    });
    expect(providerDefinition("llamacpp")).toMatchObject({
      api: "chat",
      baseUrl: "http://127.0.0.1:8080/v1",
      apiKeyOptional: true,
    });
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
