import { readFile } from "node:fs/promises";
import { join } from "node:path";
import type { AgentPrompt, PromptLoaderPort } from "@nusashell/application";

const STATIC_PROMPT_FILES = ["system.md", "mcp-tools.md"] as const;
const DEVELOPER_PROMPT_FILE = "developer.md";
const COMPACT_PROMPT_FILE = "compact.md";

/**
 * Loads agent prompt files from a filesystem directory. Static prompts
 * (system.md, mcp-tools.md) are returned as-is; developer.md is flagged
 * as a template for {{var}} substitution. compact.md is loaded lazily
 * only when compaction runs.
 */
export class FilesystemPromptLoader implements PromptLoaderPort {
  private cachedPrompts: readonly AgentPrompt[] | undefined;
  private cachedCompact: string | undefined | null;

  constructor(private readonly promptsRoot: string) {}

  async loadPrompts(): Promise<readonly AgentPrompt[]> {
    if (this.cachedPrompts) return this.cachedPrompts;
    const prompts: AgentPrompt[] = [];
    for (const file of STATIC_PROMPT_FILES) {
      const content = await this.readPromptFile(file);
      prompts.push({ name: file.replace(/\.md$/, ""), content, isTemplate: false });
    }
    const developerContent = await this.readPromptFile(DEVELOPER_PROMPT_FILE);
    prompts.push({ name: "developer", content: developerContent, isTemplate: true });
    this.cachedPrompts = prompts;
    return prompts;
  }

  async loadCompactPrompt(): Promise<string | undefined> {
    if (this.cachedCompact !== undefined && this.cachedCompact !== null) {
      return this.cachedCompact ?? undefined;
    }
    try {
      this.cachedCompact = await readFile(join(this.promptsRoot, COMPACT_PROMPT_FILE), "utf8");
      return this.cachedCompact;
    } catch {
      this.cachedCompact = null;
      return undefined;
    }
  }

  private async readPromptFile(name: string): Promise<string> {
    try {
      return await readFile(join(this.promptsRoot, name), "utf8");
    } catch {
      throw new Error(`Missing prompt file: ${join(this.promptsRoot, name)}`);
    }
  }
}
