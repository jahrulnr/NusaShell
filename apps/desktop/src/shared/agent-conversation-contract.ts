interface AgentConversationAttachmentBase {
  readonly mediaType: string;
  readonly name: string;
}

export type AgentConversationAttachment =
  | (AgentConversationAttachmentBase & { readonly type: "image" | "file"; readonly dataUrl: string })
  | (AgentConversationAttachmentBase & { readonly type: "text"; readonly content: string });

export interface AgentConversationToolCall {
  readonly id: string;
  /** Stable display order inside one tool-call step. */
  readonly callPosition?: number;
  readonly name: string;
  readonly ok: boolean;
  readonly error?: string;
  readonly args?: Readonly<Record<string, unknown>>;
  readonly output?: string;
  /** Exact mid-turn projection when available; rehydrate prefers this. */
  readonly modelOutput?: string;
  /** Canonical status from AgentToolResult. */
  readonly status?: "success" | "error" | "cancelled" | "timeout";
  /** Whether the projection was truncated. */
  readonly truncated?: boolean;
  /** Bounded structured content (omitted if oversized). */
  readonly structuredContent?: Record<string, unknown>;
}

export type AgentConversationStep =
  | { readonly type: "reasoning"; readonly stepPosition?: number; readonly content: string }
  | { readonly type: "tool_calls"; readonly stepPosition?: number; readonly calls: readonly AgentConversationToolCall[] }
  | { readonly type: "text"; readonly stepPosition?: number; readonly content: string };

export interface AgentConversationMessage {
  /** Immutable durable bubble identity. Optional only on legacy/input drafts. */
  readonly id?: string;
  /** Strictly increasing durable order within one conversation. */
  readonly position?: number;
  /** Monotonic durable replacement version for this message identity. */
  readonly revision?: number;
  readonly role: "user" | "assistant";
  readonly content: string;
  readonly attachments?: readonly AgentConversationAttachment[];
  readonly createdAt?: string;
  readonly updatedAt?: string;
  readonly traceId?: string;
  readonly model?: string;
  readonly rounds?: number;
  readonly reasoning?: string;
  readonly toolCalls?: readonly AgentConversationToolCall[];
  readonly steps?: readonly AgentConversationStep[];
  readonly status?: "complete" | "interrupted";
  readonly resumeMessages?: readonly unknown[];
  /** Why the turn was interrupted — used by the UI to pick Resume vs Continue. */
  readonly interruptReason?: "cancel" | "provider" | "max_rounds";
  /** Durable provider failure with no partial output; Retry restarts the turn. */
  readonly retryOnly?: boolean;
}

export interface AgentAssistantReservation {
  readonly messageId: string;
  readonly position: number;
  /** Current durable revision; zero means the slot has not materialized yet. */
  readonly revision: number;
}

export interface AgentConversationCheckpoint {
  readonly summary: string;
  readonly compactedMessageCount: number;
  /** Immutable transcript boundary; preferred over the legacy array count. */
  readonly compactedThroughPosition?: number;
  readonly via: "provider" | "extractive";
  /** Number of compaction events recorded for this room. Optional for legacy checkpoints. */
  readonly compactionCount?: number;
}

export interface AgentConversationAcp {
  readonly providerId: string;
  readonly sessionId?: string;
  readonly workspace?: string;
}
export interface AgentConversationModelBinding {
  readonly modelKey: string;
  readonly effort: string;
  /** Whether the model was explicitly picked for this room (vs global default). */
  readonly explicit?: boolean;
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
  readonly model?: AgentConversationModelBinding;
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
