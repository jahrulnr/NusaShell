export interface AgentPrompt {
  readonly name: string;
  readonly content: string;
  readonly isTemplate: boolean;
}

export type ReviewPromptKind = "memory" | "skill" | "combined";

export interface PromptLoaderPort {
  loadPrompts(): Promise<readonly AgentPrompt[]>;
  loadCompactPrompt(): Promise<string | undefined>;
  loadSubagentPrompt(): Promise<string | undefined>;
  loadReviewPrompt(kind: ReviewPromptKind): Promise<string>;
}
