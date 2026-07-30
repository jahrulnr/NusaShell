interface AgentConversationAttachmentBase {
  readonly mediaType: string;
  readonly name: string;
}

export type AgentConversationAttachment =
  | (AgentConversationAttachmentBase & { readonly type: "image" | "file"; readonly dataUrl: string })
  | (AgentConversationAttachmentBase & { readonly type: "text"; readonly content: string });

export interface AgentConversationToolCall {
  readonly id: string;
  readonly name: string;
  readonly ok: boolean;
  readonly error?: string;
}

export type AgentConversationStep =
  | { readonly type: "reasoning"; readonly content: string }
  | { readonly type: "tool_calls"; readonly calls: readonly AgentConversationToolCall[] }
  | { readonly type: "text"; readonly content: string };

export interface AgentConversationMessage {
  readonly role: "user" | "assistant";
  readonly content: string;
  readonly attachments?: readonly AgentConversationAttachment[];
  readonly createdAt?: string;
  readonly traceId?: string;
  readonly model?: string;
  readonly rounds?: number;
  readonly reasoning?: string;
  readonly toolCalls?: readonly AgentConversationToolCall[];
  readonly steps?: readonly AgentConversationStep[];
}

export interface AgentConversationCheckpoint {
  readonly summary: string;
  readonly compactedMessageCount: number;
  readonly via: "provider" | "extractive";
}

export interface AgentConversation {
  readonly id: string;
  readonly title: string;
  readonly createdAt: string;
  readonly updatedAt: string;
  readonly messages: readonly AgentConversationMessage[];
  readonly checkpoint?: AgentConversationCheckpoint;
}

export type AgentConversationSummary = Omit<AgentConversation, "messages" | "checkpoint"> & {
  readonly messageCount: number;
};
