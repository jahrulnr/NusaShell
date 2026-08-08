import { readFile } from "node:fs/promises";
import { join } from "node:path";
import type { AgentPrompt, PromptLoaderPort, ReviewPromptKind } from "@nusashell/application";

const STATIC_PROMPT_FILES = ["system.md", "mcp-tools.md"] as const;
const COMPACT_PROMPT_FILE = "compact.md";
const SUBAGENT_EXECUTION_PROMPT_FILE = "subagent.md";
const SUBAGENT_DELEGATION_PROMPT_FILE = "subagent-delegation.md";
const CONTINUE_PROMPT_FILE = "continue.md";
const REVIEW_PROMPT_FILES: Record<ReviewPromptKind, string> = {
  memory: "memory-review.md",
  skill: "skill-review.md",
  combined: "combined-review.md",
};

/**
 * Loads agent prompt files from a filesystem directory. Static prompts
 * (system.md, mcp-tools.md) are returned as the cache-stable prefix.
 * Dynamic runtime facts arrive through the ephemeral hydration transcript;
 * compact.md is loaded lazily only when compaction runs. Review prompts are
 * loaded on demand.
 */
export class FilesystemPromptLoader implements PromptLoaderPort {
  private cachedPrompts: readonly AgentPrompt[] | undefined;
  private cachedCompact: string | undefined | null;
  private cachedSubagent: string | undefined | null;
  private cachedSubagentExecution: string | undefined | null;
  private cachedContinue: string | undefined | null;
  private readonly cachedReview = new Map<ReviewPromptKind, string>();

  constructor(private readonly promptsRoot: string) {}

  async loadPrompts(): Promise<readonly AgentPrompt[]> {
    if (this.cachedPrompts) return this.cachedPrompts;
    const prompts: AgentPrompt[] = [];
    for (const file of STATIC_PROMPT_FILES) {
      const content = await this.readPromptFile(file);
      prompts.push({ name: file.replace(/\.md$/, ""), content, isTemplate: false });
    }
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

  async loadSubagentPrompt(): Promise<string | undefined> {
    if (this.cachedSubagent !== undefined && this.cachedSubagent !== null) {
      return this.cachedSubagent ?? undefined;
    }
    try {
      this.cachedSubagent = await readFile(join(this.promptsRoot, SUBAGENT_DELEGATION_PROMPT_FILE), "utf8");
      return this.cachedSubagent;
    } catch {
      this.cachedSubagent = null;
      return undefined;
    }
  }

  async loadSubagentExecutionPrompt(): Promise<string | undefined> {
    if (this.cachedSubagentExecution !== undefined && this.cachedSubagentExecution !== null) {
      return this.cachedSubagentExecution ?? undefined;
    }
    try {
      this.cachedSubagentExecution = await readFile(join(this.promptsRoot, SUBAGENT_EXECUTION_PROMPT_FILE), "utf8");
      return this.cachedSubagentExecution;
    } catch {
      this.cachedSubagentExecution = null;
      return undefined;
    }
  }

  async loadContinuePrompt(): Promise<string | undefined> {
    if (this.cachedContinue !== undefined && this.cachedContinue !== null) {
      return this.cachedContinue ?? undefined;
    }
    try {
      this.cachedContinue = await readFile(join(this.promptsRoot, CONTINUE_PROMPT_FILE), "utf8");
      return this.cachedContinue;
    } catch {
      this.cachedContinue = null;
      return undefined;
    }
  }

  async loadReviewPrompt(kind: ReviewPromptKind): Promise<string> {
    const cached = this.cachedReview.get(kind);
    if (cached) return cached;
    const fileName = REVIEW_PROMPT_FILES[kind];
    const content = await this.readPromptFile(fileName);
    this.cachedReview.set(kind, content);
    return content;
  }

  private async readPromptFile(name: string): Promise<string> {
    try {
      return await readFile(join(this.promptsRoot, name), "utf8");
    } catch {
      throw new Error(`Missing prompt file: ${join(this.promptsRoot, name)}`);
    }
  }
}
