export interface AgentPrompt {
  readonly name: string;
  readonly content: string;
  readonly isTemplate: boolean;
}

export interface PromptLoaderPort {
  loadPrompts(): Promise<readonly AgentPrompt[]>;
  loadCompactPrompt(): Promise<string | undefined>;
}
