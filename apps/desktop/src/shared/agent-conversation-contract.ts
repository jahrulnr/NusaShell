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
  readonly args?: Readonly<Record<string, unknown>>;
  readonly output?: string;
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
  readonly status?: "complete" | "interrupted";
  readonly resumeMessages?: readonly unknown[];
}

export interface AgentConversationCheckpoint {
  readonly summary: string;
  readonly compactedMessageCount: number;
  readonly via: "provider" | "extractive";
}

export interface AgentConversationAcp {
  readonly providerId: string;
  readonly sessionId?: string;
  readonly workspace?: string;
}

export type AgentConversationKind = "agent" | "acp";

export type AgentCanvasArtifactKind = "html" | "svg" | "mermaid";

export interface AgentCanvasArtifact {
  readonly id: string;
  readonly conversationId: string;
  readonly sourceMessageId: string;
  readonly fenceIndex: number;
  readonly kind: AgentCanvasArtifactKind;
  readonly title: string;
  readonly source: string;
  readonly createdAt: string;
  readonly updatedAt: string;
}

export type AgentSubagentRunStatus = "running" | "ok" | "fail" | "cancelled";

/** Chronological subagent stream segments for side-pane replay. */
export type AgentSubagentStreamStep =
  | AgentConversationStep
  | {
      readonly type: "plan";
      readonly steps: readonly { readonly text: string; readonly done?: boolean }[];
    };

export interface AgentSubagentRun {
  readonly id: string;
  readonly conversationId: string;
  readonly sourceMessageId: string;
  readonly runId: string;
  readonly providerId: string;
  readonly title?: string;
  readonly prompt: string;
  readonly status: AgentSubagentRunStatus;
  readonly summary?: string;
  readonly error?: string;
  readonly attempted?: readonly string[];
  /** Persisted live stream (text / reasoning / tools / plan) for review after the run. */
  readonly steps?: readonly AgentSubagentStreamStep[];
  readonly createdAt: string;
  readonly updatedAt: string;
}

export interface AgentConversation {
  readonly id: string;
  readonly title: string;
  readonly createdAt: string;
  readonly updatedAt: string;
  readonly messages: readonly AgentConversationMessage[];
  readonly checkpoint?: AgentConversationCheckpoint;
  readonly workspace?: string;
  readonly kind?: AgentConversationKind;
  readonly acp?: AgentConversationAcp;
  readonly canvasArtifacts?: readonly AgentCanvasArtifact[];
  readonly activeCanvasArtifactId?: string;
  readonly subagentRuns?: readonly AgentSubagentRun[];
  readonly activeSubagentRunId?: string;
}

export type AgentConversationSummary = Omit<AgentConversation, "messages" | "checkpoint"> & {
  readonly messageCount: number;
};
