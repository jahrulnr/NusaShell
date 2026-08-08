import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { FilesystemPromptLoader } from "../src/agent/filesystem-prompt-loader.js";

const fixturesRoot = join(
  fileURLToPath(import.meta.url),
  "..",
  "fixtures",
  "prompts",
);

describe("FilesystemPromptLoader", () => {
  it("loads only the cache-stable default prompt pair", async () => {
    const loader = new FilesystemPromptLoader(fixturesRoot);

    await expect(loader.loadPrompts()).resolves.toMatchObject([
      { name: "system", isTemplate: false },
      { name: "mcp-tools", isTemplate: false },
    ]);
  });

  it("loads static prompts (system, mcp-tools) as non-template", async () => {
    const loader = new FilesystemPromptLoader(fixturesRoot);
    const prompts = await loader.loadPrompts();

    const system = prompts.find((p) => p.name === "system");
    expect(system).toBeDefined();
    expect(system!.isTemplate).toBe(false);
    expect(system!.content).toContain("NusaShell test agent");

    const mcpTools = prompts.find((p) => p.name === "mcp-tools");
    expect(mcpTools).toBeDefined();
    expect(mcpTools!.isTemplate).toBe(false);
  });

  it("caches prompts after the first load", async () => {
    const loader = new FilesystemPromptLoader(fixturesRoot);
    const first = await loader.loadPrompts();
    const second = await loader.loadPrompts();
    expect(second).toBe(first);
  });

  it("loads the subagent prompt with template placeholders", async () => {
    const loader = new FilesystemPromptLoader(fixturesRoot);
    const subagent = await loader.loadSubagentPrompt();

    expect(subagent).toBeDefined();
    expect(subagent).toContain("{{available_subagents}}");
    expect(subagent).toContain("{{default_subagent}}");
  });

  it("loads the ACP execution contract separately from parent delegation guidance", async () => {
    const loader = new FilesystemPromptLoader(fixturesRoot);

    await expect(loader.loadSubagentExecutionPrompt()).resolves.toContain("You are a dumb pipe.");
  });

  it("returns undefined when subagent prompt file is missing", async () => {
    const loader = new FilesystemPromptLoader(join(fixturesRoot, "..", "no-such-dir"));
    await expect(loader.loadSubagentPrompt()).resolves.toBeUndefined();
  });

  it("loads the compact prompt", async () => {
    const loader = new FilesystemPromptLoader(fixturesRoot);
    const compact = await loader.loadCompactPrompt();

    expect(compact).toBeDefined();
    expect(compact).toContain("Compaction summary");
  });

  it("returns undefined when compact prompt file is missing", async () => {
    const loader = new FilesystemPromptLoader(join(fixturesRoot, "..", "no-such-dir"));
    await expect(loader.loadCompactPrompt()).resolves.toBeUndefined();
  });

  it("throws when a required static prompt file is missing", async () => {
    const loader = new FilesystemPromptLoader(join(fixturesRoot, "..", "no-such-dir"));
    await expect(loader.loadPrompts()).rejects.toThrow(/Missing prompt file/);
  });
});
