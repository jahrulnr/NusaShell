import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it, vi } from "vitest";

vi.mock("electron", () => ({
  safeStorage: {
    isEncryptionAvailable: () => true,
    encryptString: (value: string) => Buffer.from(`encrypted:${value}`),
    decryptString: (value: Buffer) => value.toString().replace(/^encrypted:/, ""),
  },
}));

import { AiSettingsStore } from "../src/main/ai-settings.js";

describe("AiSettingsStore", () => {
  it("saves a configured provider without requiring a default model and never exposes its key", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-ai-settings-"));
    const path = join(directory, "ai-settings.json");
    const store = new AiSettingsStore(path, join(directory, "user-prompt.md"));

    const result = await store.saveProvider({
      id: "omniroute",
      name: "OmniRoute",
      type: "openai-compatible",
      api: "chat",
      baseUrl: "https://gateway.example/v1",
      apiKey: "secret-key",
      apiKeyOptional: false,
      enabled: true,
      defaultModel: "",
    });

    expect(result.providers[0]).toMatchObject({
      id: "omniroute",
      defaultModel: "",
      hasApiKey: true,
    });
    expect(result.providers[0]).not.toHaveProperty("apiKey");
    expect(await readFile(path, "utf8")).not.toContain("secret-key");
  });

  it("preserves a saved key when provider details are updated with a blank key", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-ai-settings-"));
    const store = new AiSettingsStore(join(directory, "ai-settings.json"), join(directory, "user-prompt.md"));
    await store.saveProvider({
      id: "openrouter",
      name: "OpenRouter",
      type: "openai-compatible",
      api: "chat",
      baseUrl: "https://openrouter.ai/api/v1",
      apiKey: "secret-key",
      apiKeyOptional: false,
      enabled: true,
      defaultModel: "",
    });

    await store.saveProvider({
      id: "openrouter",
      name: "OpenRouter",
      type: "openai-compatible",
      api: "chat",
      baseUrl: "https://openrouter.ai/api/v1",
      apiKey: "",
      apiKeyOptional: false,
      enabled: true,
      defaultModel: "",
    });

    const loaded = await store.load();
    expect(loaded.providers[0]?.apiKey).toBe("secret-key");
  });

  it("clears a selected model and moves the active provider when its provider is disabled", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-ai-settings-"));
    const store = new AiSettingsStore(join(directory, "ai-settings.json"), join(directory, "user-prompt.md"));
    for (const id of ["first", "second"]) {
      await store.saveProvider({
        id,
        name: id,
        type: "openai-compatible",
        api: "chat",
        baseUrl: `https://${id}.example/v1`,
        apiKey: "secret-key",
        apiKeyOptional: false,
        enabled: true,
      });
      await store.addModel(id, { id: `${id}/model`, label: `${id} model` });
    }
    await store.select({ modelKey: "second::second/model", effort: "high" });

    const result = await store.saveProvider({
      id: "second",
      name: "second",
      type: "openai-compatible",
      api: "chat",
      baseUrl: "https://second.example/v1",
      apiKey: "",
      apiKeyOptional: false,
      enabled: false,
    });

    expect(result.activeProviderId).toBe("first");
    expect(result.activeModelKey).toBe("");
    expect(result.effort).toBe("auto");
  });

  it("deletes a provider, its models, credential, and stale active selection", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-ai-settings-"));
    const path = join(directory, "ai-settings.json");
    const userPromptPath = join(directory, "user-prompt.md");
    const store = new AiSettingsStore(path, userPromptPath);
    await store.saveProvider({
      id: "openrouter",
      name: "OpenRouter",
      type: "openai-compatible",
      api: "chat",
      baseUrl: "https://openrouter.ai/api/v1",
      apiKey: "remaining-secret",
      apiKeyOptional: false,
      enabled: true,
    });
    await store.saveProvider({
      id: "omniroute",
      name: "OmniRoute",
      type: "openai-compatible",
      api: "responses",
      baseUrl: "https://gateway.example/v1",
      apiKey: "deleted-secret",
      apiKeyOptional: false,
      enabled: true,
    });
    await store.addModel("openrouter", { id: "openrouter/model", label: "OpenRouter model" });
    await store.addModel("omniroute", { id: "omni/model", label: "Omni model" });
    await store.select({ modelKey: "omniroute::omni/model", effort: "high" });

    const result = await store.deleteProvider("omniroute");

    expect(result.providers.map((provider) => provider.id)).toEqual(["openrouter"]);
    expect(result.models.map((model) => model.key)).toEqual(["openrouter::openrouter/model"]);
    expect(result.activeProviderId).toBe("openrouter");
    expect(result.activeModelKey).toBe("");
    expect(result.effort).toBe("auto");
    const reloaded = await new AiSettingsStore(path, userPromptPath).load();
    expect(reloaded.providers.map((provider) => provider.id)).toEqual(["openrouter"]);
    expect(reloaded.providers[0]?.apiKey).toBe("remaining-secret");
    expect(await readFile(path, "utf8")).not.toContain("omniroute");
  });

  it("treats deleting an unknown provider as an idempotent no-op", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-ai-settings-"));
    const store = new AiSettingsStore(join(directory, "ai-settings.json"), join(directory, "user-prompt.md"));

    await expect(store.deleteProvider("missing")).resolves.toMatchObject({
      activeProviderId: "",
      activeModelKey: "",
      providers: [],
    });
  });

  it("persists hot-reloadable routing, streaming, and vision settings", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-ai-settings-"));
    const path = join(directory, "ai-settings.json");
    const userPromptPath = join(directory, "user-prompt.md");
    const store = new AiSettingsStore(path, userPromptPath);

    const result = await store.updateRuntime({
      strategy: "round-robin",
      totalAttemptBudget: 7,
      stream: false,
      vision: "off",
      userPrompt: "Be concise.",
    });

    expect(result).toMatchObject({
      strategy: "round-robin",
      totalAttemptBudget: 7,
      stream: false,
      vision: "off",
      userPrompt: "Be concise.",
    });
    await expect(new AiSettingsStore(path, userPromptPath).load()).resolves.toMatchObject({
      strategy: "round-robin",
      totalAttemptBudget: 7,
      stream: false,
      vision: "off",
      userPrompt: "Be concise.",
    });
    await expect(readFile(userPromptPath, "utf8")).resolves.toBe("Be concise.");
  });

  it("does not replace a corrupt provider registry with empty settings", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-ai-settings-"));
    const path = join(directory, "ai-settings.json");
    await writeFile(path, "{not-json", "utf8");

    await expect(new AiSettingsStore(path, join(directory, "user-prompt.md")).load()).rejects.toThrow("Could not load AI settings");
    expect(await readFile(path, "utf8")).toBe("{not-json");
  });
});
