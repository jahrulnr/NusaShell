export type AgentContentPart =
  | { readonly type: "text"; readonly text: string }
  | {
      readonly type: "image";
      readonly dataUrl: string;
      readonly name?: string;
      readonly detail?: "auto" | "low" | "high";
    }
  | {
      readonly type: "file";
      readonly dataUrl: string;
      readonly mediaType: string;
      readonly name: string;
    };

export type AgentMessage =
  | { readonly role: "system"; readonly content: string }
  | { readonly role: "user"; readonly content: string | readonly AgentContentPart[] }
  | {
      readonly role: "assistant";
      readonly content?: string;
      readonly toolCalls?: readonly AgentToolCall[];
      /**
       * Model reasoning emitted alongside this assistant turn. Carried through
       * the internal message log so context compaction can preserve the
       * assistant's stated decisions; provider adapters do not send it.
       */
      readonly reasoning?: string;
    }
  | {
      readonly role: "tool";
      readonly toolCallId: string;
      readonly name: string;
      readonly content: string;
      /**
       * When true, the tool result represents an execution error (not a
       * successful result). Anthropic's Messages API maps this to
       * `is_error: true` on the `tool_result` block. OpenAI adapters ignore it.
       */
      readonly toolIsError?: boolean;
    };

export type ReasoningEffort = "auto" | "none" | "minimal" | "low" | "medium" | "high" | "xhigh" | "max";

export interface AgentToolCall {
  readonly id: string;
  readonly name: string;
  readonly args: Readonly<Record<string, unknown>>;
}

export interface AgentToolDefinition {
  readonly name: string;
  readonly description?: string;
  readonly inputSchema?: Readonly<Record<string, unknown>>;
}

export interface AgentProviderRequest {
  readonly traceId: string;
  readonly round: number;
  readonly messages: readonly AgentMessage[];
  readonly tools: readonly AgentToolDefinition[];
  readonly model?: string;
  readonly effort?: ReasoningEffort;
  readonly modelCapabilities?: AgentModelCapabilities;
  readonly signal?: AbortSignal;
  readonly onTextDelta?: (delta: string) => void;
  readonly onReasoningDelta?: (delta: string) => void;
  /** Router-owned global HTTP-attempt budget. Providers consume before I/O. */
  readonly consumeAttempt?: () => boolean;
}

export interface AgentModelCapabilities {
  readonly contextWindow?: number;
  readonly maxOutput?: number;
  readonly inputModes?: readonly string[];
  readonly outputModes?: readonly string[];
  readonly supportedEfforts?: readonly ReasoningEffort[];
  readonly defaultEffort?: ReasoningEffort;
  readonly reasoningSupported?: boolean;
  readonly reasoningMandatory?: boolean;
  readonly reasoningSupportsMaxTokens?: boolean;
  readonly supportsTools?: boolean;
  readonly supportsVision?: boolean;
}

export interface AgentTokenUsage {
  readonly inputTokens: number;
  readonly outputTokens: number;
  readonly cachedInputTokens: number;
  readonly cacheWriteTokens: number;
  readonly reasoningOutputTokens: number;
}

export interface AgentProviderResult {
  readonly text?: string;
  readonly toolCalls?: readonly AgentToolCall[];
  readonly reasoning?: string;
  readonly model?: string;
  readonly providerId?: string;
  readonly api?: "chat" | "responses" | "messages";
  readonly status?: string;
  readonly usage?: AgentTokenUsage;
}

export interface AgentProvider {
  readonly id: string;
  readonly managesAttemptBudget?: boolean;
  complete(request: AgentProviderRequest): Promise<AgentProviderResult>;
}

export interface AgentProviderRegistryPort {
  get(providerId: string): AgentProvider | undefined;
  list(): readonly AgentProvider[];
}
