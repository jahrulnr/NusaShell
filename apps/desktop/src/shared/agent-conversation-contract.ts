export interface AgentConversationAttachment {
  readonly type: "image" | "file";
  readonly dataUrl: string;
  readonly mediaType: string;
  readonly name: string;
}

export interface AgentConversationMessage {
  readonly role: "user" | "assistant";
  readonly content: string;
  readonly attachments?: readonly AgentConversationAttachment[];
  readonly createdAt?: string;
  readonly traceId?: string;
  readonly model?: string;
  readonly rounds?: number;
  readonly toolCalls?: readonly {
    readonly id: string;
    readonly name: string;
    readonly ok: boolean;
    readonly error?: string;
  }[];
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
