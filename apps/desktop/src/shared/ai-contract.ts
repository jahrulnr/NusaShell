export type ReasoningEffort = "auto" | "none" | "minimal" | "low" | "medium" | "high" | "xhigh" | "max";
export type AiProviderType =
  | "openrouter"
  | "omniroute"
  | "9router"
  | "openai"
  | "claude"
  | "openai-compatible";
export type AiProviderApi = "chat" | "responses" | "messages";

export interface AiModelSettings {
  readonly id: string;
  readonly label: string;
  readonly task: string;
  readonly contextWindow?: number;
  readonly maxOutput?: number;
  readonly inputModes: readonly string[];
  readonly outputModes: readonly string[];
  readonly supportedEfforts: readonly ReasoningEffort[];
  readonly defaultEffort: ReasoningEffort;
  readonly reasoningSupported?: boolean;
  readonly reasoningMandatory?: boolean;
  readonly reasoningSupportsMaxTokens?: boolean;
  /** Undefined means the provider catalog did not advertise this capability. */
  readonly supportsTools?: boolean;
  readonly description?: string;
}

export interface AiProviderSettings {
  readonly id: string;
  readonly name: string;
  readonly type: AiProviderType;
  readonly api: AiProviderApi;
  readonly baseUrl: string;
  readonly apiKey?: string;
  readonly apiKeyOptional: boolean;
  readonly enabled: boolean;
  readonly defaultModel: string;
  readonly timeoutMs: number;
  readonly maxAttempts: number;
  readonly weight: number;
  readonly models: readonly AiModelSettings[];
}

export interface AiRegistrySettings {
  readonly activeProviderId: string;
  readonly activeModelKey: string;
  readonly effort: ReasoningEffort;
  readonly strategy: "failover" | "round-robin" | "switch";
  readonly totalAttemptBudget: number;
  readonly stream: boolean;
  readonly vision: "auto" | "on" | "off";
  readonly providers: readonly AiProviderSettings[];
}

export interface AgentModelOption extends AiModelSettings {
  readonly key: string;
  readonly providerId: string;
  readonly providerName: string;
}

export interface SaveAiProviderInput {
  readonly id: string;
  readonly name: string;
  readonly type: AiProviderType;
  readonly api: AiProviderApi;
  readonly baseUrl: string;
  readonly apiKey?: string;
  readonly apiKeyOptional: boolean;
  readonly enabled: boolean;
  readonly defaultModel?: string;
  readonly timeoutMs?: number;
  readonly maxAttempts?: number;
  readonly weight?: number;
}

export interface PublicAiProvider extends Omit<AiProviderSettings, "apiKey"> {
  readonly hasApiKey: boolean;
}

export interface PublicAiRegistry {
  readonly activeProviderId: string;
  readonly activeModelKey: string;
  readonly effort: ReasoningEffort;
  readonly strategy: AiRegistrySettings["strategy"];
  readonly totalAttemptBudget: number;
  readonly stream: boolean;
  readonly vision: AiRegistrySettings["vision"];
  readonly canPersistApiKey: boolean;
  readonly providers: readonly PublicAiProvider[];
  readonly models: readonly AgentModelOption[];
}
