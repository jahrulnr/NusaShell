import { describe, expect, it, vi } from "vitest";
import {
  flattenModelCatalog,
  importProviderModels,
  normalizeRegistryState,
  type AiProviderSettings,
} from "../src/main/ai-provider-registry.js";

describe("AI provider registry", () => {
  it("keeps an empty production registry instead of inventing a visible stub provider", () => {
    expect(normalizeRegistryState({})).toEqual({
      activeProviderId: "",
      activeModelKey: "",
      effort: "auto",
      strategy: "failover",
      totalAttemptBudget: 4,
      stream: true,
      vision: "auto",
      userPrompt: "",
      maxToolRounds: 50,
      maxRepeatedToolCalls: 50,
      maxAutoContinues: 10,
      compactionEnabled: true,
      maxInputTokens: 200_000,
      reserveTokens: 16_000,
      recentTurns: 4,
      summaryMaxChars: 12000,
      providers: [],
    });
  });

  it("migrates legacy settings without requiring a default model", () => {
    const state = normalizeRegistryState({
      providerId: "openai-compatible",
      model: "",
      baseUrl: "https://provider.example/v1",
      effort: "auto",
      apiKey: "secret",
    });

    expect(state.activeProviderId).toBe("openai-compatible");
    expect(state.activeModelKey).toBe("");
    expect(state.providers[0]).toMatchObject({
      id: "openai-compatible",
      defaultModel: "",
      baseUrl: "https://provider.example/v1",
    });
  });

  it("flattens every enabled provider model into the agent catalog", () => {
    const providers: AiProviderSettings[] = [
      provider("openrouter", "OpenRouter", [{
        id: "openai/gpt-5",
        label: "GPT-5",
        task: "chat",
        inputModes: ["text"],
        outputModes: ["text"],
        supportedEfforts: ["low", "medium", "high"],
        defaultEffort: "medium",
        supportsTools: true,
      }]),
      provider("omniroute", "OmniRoute", [{
        id: "anthropic/claude-sonnet",
        label: "Claude Sonnet",
        task: "chat",
        inputModes: ["text", "image", "file"],
        outputModes: ["text"],
        supportedEfforts: [],
        defaultEffort: "auto",
        supportsTools: true,
      }]),
    ];

    expect(flattenModelCatalog(providers)).toEqual([
      expect.objectContaining({ key: "openrouter::openai/gpt-5", providerId: "openrouter", providerName: "OpenRouter" }),
      expect.objectContaining({ key: "omniroute::anthropic/claude-sonnet", providerId: "omniroute", providerName: "OmniRoute" }),
    ]);
  });

  it("imports and normalizes model capabilities from an OpenAI-compatible models endpoint", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      data: [{
        id: "openai/gpt-5",
        name: "GPT-5",
        description: "Reasoning model",
        context_length: 400000,
        architecture: {
          input_modalities: ["text", "image", "file"],
          output_modalities: ["text"],
        },
        supported_parameters: ["tools", "reasoning_effort"],
        reasoning: {
          supported_efforts: ["minimal", "low", "medium", "high", "xhigh"],
          default_effort: "medium",
          mandatory: true,
          supports_max_tokens: true,
        },
      }],
    }), { status: 200, headers: { "content-type": "application/json" } }));

    const models = await importProviderModels(provider("openrouter", "OpenRouter", []), fetchFn);

    expect(fetchFn).toHaveBeenCalledWith("https://openrouter.example/v1/models", expect.objectContaining({
      headers: expect.objectContaining({ authorization: "Bearer secret" }),
    }));
    expect(models).toEqual([expect.objectContaining({
      id: "openai/gpt-5",
      contextWindow: 400000,
      inputModes: ["text", "image", "file"],
      outputModes: ["text"],
      supportedEfforts: ["minimal", "low", "medium", "high", "xhigh"],
      defaultEffort: "medium",
      reasoningSupported: true,
      reasoningMandatory: true,
      reasoningSupportsMaxTokens: true,
      supportsTools: true,
      supportsVision: true,
    })]);
  });

  it("preserves an explicitly advertised lack of image input", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      data: [{
        id: "deepseek/deepseek-v4-flash:free",
        capabilities: { image_input: { supported: false } },
      }],
    }), { status: 200 }));

    const models = await importProviderModels(provider("gateway", "Gateway", []), fetchFn);

    expect(models[0]).toMatchObject({ supportsVision: false });
  });

  it("keeps vision unknown when a gateway catalog only returns model IDs", () => {
    const state = normalizeRegistryState({
      providers: [{
        ...provider("gateway", "Gateway", []),
        models: [
          { id: "agy/claude-opus-4-6-thinking", label: "Claude Opus", task: "", inputModes: [], outputModes: [], supportedEfforts: [], defaultEffort: "auto" },
          { id: "agy/claude-sonnet-4-6", label: "Claude Sonnet", task: "", inputModes: [], outputModes: [], supportedEfforts: [], defaultEffort: "auto" },
          { id: "agy/gemini-2.5-flash", label: "Gemini", task: "", inputModes: [], outputModes: [], supportedEfforts: [], defaultEffort: "auto" },
          { id: "oc/deepseek-v4-flash-free", label: "DeepSeek", task: "", inputModes: [], outputModes: [], supportedEfforts: [], defaultEffort: "auto" },
        ],
      }],
    });

    expect(state.providers[0]?.models).toMatchObject([
      { id: "agy/claude-opus-4-6-thinking" },
      { id: "agy/claude-sonnet-4-6" },
      { id: "agy/gemini-2.5-flash" },
      { id: "oc/deepseek-v4-flash-free" },
    ]);
    expect(state.providers[0]?.models[0]).not.toHaveProperty("supportsVision");
    expect(state.providers[0]?.models[1]).not.toHaveProperty("supportsVision");
    expect(state.providers[0]?.models[2]).not.toHaveProperty("supportsVision");
    expect(state.providers[0]?.models[3]).not.toHaveProperty("supportsVision");
  });

  it("records explicit text-only input modality metadata", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      data: [{
        id: "deepseek/deepseek-chat",
        architecture: { input_modalities: ["text"] },
      }],
    }), { status: 200 }));

    const models = await importProviderModels(provider("gateway", "Gateway", []), fetchFn);

    expect(models[0]).toMatchObject({ supportsVision: false });
  });

  it("treats supported_parameters reasoning as advertised effort support", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      data: [{
        id: "gateway/reasoner",
        supported_parameters: ["reasoning", "tools"],
      }],
    }), { status: 200 }));

    const models = await importProviderModels(provider("gateway", "Gateway", []), fetchFn);

    expect(models[0]).toMatchObject({
      reasoningSupported: true,
      supportedEfforts: [],
      defaultEffort: "medium",
      supportsTools: true,
    });
  });

  it("keeps omitted tool capability unknown instead of disabling MCP tools", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      data: [{ id: "official/model-with-minimal-metadata", object: "model" }],
    }), { status: 200 }));

    const models = await importProviderModels(provider("gateway", "Gateway", []), fetchFn);

    expect(models[0]).not.toHaveProperty("supportsTools");
  });

  it("rejects non-model catalog entries and pagination that loops", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      data: [
        { id: "valid", object: "model" },
        { id: "not-a-model", object: "assistant" },
      ],
      links: { next: "/v1/models" },
    }), { status: 200 }));

    const models = await importProviderModels(provider("gateway", "Gateway", []), fetchFn);

    expect(models.map((model) => model.id)).toEqual(["valid"]);
    expect(fetchFn).toHaveBeenCalledTimes(1);
  });

  it("falls back to Ollama /api/tags when /v1/models fails", async () => {
    const fetchFn = vi.fn<typeof fetch>()
      .mockRejectedValueOnce(new Error("ECONNREFUSED"))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        models: [
          { name: "llama3.2:latest" },
          { name: "qwen2.5:latest" },
        ],
      }), { status: 200, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        capabilities: ["vision"],
        parameters: { num_ctx: 8192 },
      }), { status: 200, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        capabilities: ["tools"],
      }), { status: 200, headers: { "content-type": "application/json" } }));

    const models = await importProviderModels(ollamaProvider(), fetchFn);

    expect(models.map((m) => m.id)).toEqual(["llama3.2:latest", "qwen2.5:latest"]);
    expect(models[0]).toMatchObject({ supportsVision: true, contextWindow: 8192 });
    expect(models[1]).toMatchObject({ supportsTools: true });
  });

  it("maps llama.cpp path-like model ids to basename labels and meta context", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      data: [{
        id: "../models/Meta-Llama-3.1-8B-Instruct-Q4_K_M.gguf",
        object: "model",
        meta: { n_ctx_train: 131072, n_params: 8030261312 },
      }],
    }), { status: 200 }));

    const models = await importProviderModels(llamacppProvider(), fetchFn);

    expect(models[0]).toMatchObject({
      id: "../models/Meta-Llama-3.1-8B-Instruct-Q4_K_M.gguf",
      label: "Meta-Llama-3.1-8B-Instruct-Q4_K_M.gguf",
      contextWindow: 131072,
      supportsTools: true,
    });
  });

  it("wraps Ollama connection errors with actionable copy", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockRejectedValue(new Error("ECONNREFUSED"));
    await expect(importProviderModels(ollamaProvider(), fetchFn)).rejects.toThrow(/ollama serve/);
  });

  it("wraps llama.cpp connection errors with actionable copy", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockRejectedValue(new Error("ECONNREFUSED"));
    await expect(importProviderModels(llamacppProvider(), fetchFn)).rejects.toThrow(/llama-server/);
  });
});

function ollamaProvider(): AiProviderSettings {
  return {
    id: "ollama",
    name: "Ollama",
    type: "ollama",
    api: "chat",
    baseUrl: "http://127.0.0.1:11434/v1",
    apiKeyOptional: true,
    enabled: true,
    defaultModel: "",
    timeoutMs: 180_000,
    maxAttempts: 1,
    weight: 1,
    models: [],
  };
}

function llamacppProvider(): AiProviderSettings {
  return {
    id: "llamacpp",
    name: "llama.cpp",
    type: "llamacpp",
    api: "chat",
    baseUrl: "http://127.0.0.1:8080/v1",
    apiKeyOptional: true,
    enabled: true,
    defaultModel: "",
    timeoutMs: 180_000,
    maxAttempts: 1,
    weight: 1,
    models: [],
  };
}

function provider(id: string, name: string, models: AiProviderSettings["models"]): AiProviderSettings {
  return {
    id,
    name,
    type: "openai-compatible",
    api: "chat",
    baseUrl: `https://${id}.example/v1`,
    apiKey: "secret",
    apiKeyOptional: false,
    enabled: true,
    defaultModel: "",
    timeoutMs: 60_000,
    maxAttempts: 1,
    weight: 1,
    models,
  };
}
