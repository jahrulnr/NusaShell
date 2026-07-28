export type AgentMessage =
  | { readonly role: "system" | "user"; readonly content: string }
  | {
      readonly role: "assistant";
      readonly content?: string;
      readonly toolCalls?: readonly AgentToolCall[];
    }
  | {
      readonly role: "tool";
      readonly toolCallId: string;
      readonly name: string;
      readonly content: string;
    };

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
}

export interface AgentProviderResult {
  readonly text?: string;
  readonly toolCalls?: readonly AgentToolCall[];
  readonly model?: string;
}

export interface AgentProvider {
  readonly id: string;
  complete(request: AgentProviderRequest): Promise<AgentProviderResult>;
}

export interface AgentProviderRegistryPort {
  get(providerId: string): AgentProvider | undefined;
}
