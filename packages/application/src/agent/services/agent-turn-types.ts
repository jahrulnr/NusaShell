import type {
  AgentMessage,
  AgentModelCapabilities,
  AgentTokenUsage,
  AgentToolCall,
  ReasoningEffort,
} from "../ports/agent-provider.port.js";
import type { AgentToolGateway } from "../ports/agent-tool-gateway.port.js";
import type { AgentProvider } from "../ports/agent-provider.port.js";
import type { LoggerPort } from "../../plugin/ports/logger.port.js";

export const MAX_REPEATED_TOOL_CALLS = 50;
export const DEFAULT_MAX_TOOL_ROUNDS = 50;
export const DEFAULT_SOFT_RECOVER_ATTEMPTS = 1;
export const MAX_SOFT_RECOVER_ATTEMPTS = 3;
export const DEFAULT_MAX_CONCURRENT_TOOL_CALLS = 8;
export const MAX_CONCURRENT_TOOL_CALLS_CAP = 32;

/**
 * Tools that must run alone, in order (interactive barriers).
 * `ask_question` blocks the turn for user input and cannot overlap siblings.
 */
export const BARRIER_TOOLS: ReadonlySet<string> = new Set(["ask_question"]);

export interface RunAgentTurnInput {
  readonly messages: readonly AgentMessage[];
  readonly pluginIds: readonly string[];
  readonly maxToolRounds?: number;
  readonly model?: string;
  readonly effort?: ReasoningEffort;
  readonly modelCapabilities?: AgentModelCapabilities;
  readonly traceId?: string;
  readonly interactive?: boolean;
  readonly signal?: AbortSignal;
  readonly onTextDelta?: (delta: string) => void;
  readonly onReasoningDelta?: (delta: string) => void;
  readonly onToolCallStart?: (call: AgentToolCall) => void;
  readonly onToolCallEnd?: (execution: AgentToolExecution) => void;
  readonly onContextUpdate?: (update: AgentContextUpdate) => void;
}

export interface AgentContextUpdate {
  readonly estimatedTokens: number;
  readonly usage?: AgentTokenUsage;
}

export interface AgentToolExecution {
  readonly id: string;
  readonly name: string;
  readonly ok: boolean;
  readonly args?: Readonly<Record<string, unknown>>;
  readonly result?: unknown;
  readonly error?: string;
}

export type AgentTurnStep =
  | { readonly type: "reasoning"; readonly content: string; readonly model?: string; readonly providerId?: string }
  | { readonly type: "tool_calls"; readonly calls: readonly AgentToolExecution[]; readonly model?: string; readonly providerId?: string }
  | { readonly type: "text"; readonly content: string; readonly model?: string; readonly providerId?: string };

export interface AgentTurnResult {
  readonly traceId: string;
  readonly text: string;
  readonly rounds: number;
  readonly toolCalls: readonly AgentToolExecution[];
  readonly steps?: readonly AgentTurnStep[];
  readonly model?: string;
  readonly providerId?: string;
  readonly api?: "chat" | "responses" | "messages";
  readonly reasoning?: string;
  readonly usage?: AgentTokenUsage;
  readonly compaction?: AgentCompactionCheckpoint;
  readonly messages?: readonly AgentMessage[];
}

export interface AgentCompactionCheckpoint {
  readonly summary: string;
  readonly compactedMessageCount: number;
  readonly estimatedInputTokens: number;
  readonly via: "provider" | "extractive";
}

/**
 * Mid-turn progress snapshot attached to `AGENT_PROVIDER_FAILED.details.partial`
 * when a provider call fails after the turn already accumulated tool work.
 * Field names mirror `AgentTurnResult` so the desktop can treat it like a
 * result for sealing/persisting the interrupted assistant message.
 */
export interface AgentTurnPartial {
  readonly traceId: string;
  readonly rounds: number;
  readonly text: string;
  readonly toolCalls: readonly AgentToolExecution[];
  readonly steps: readonly AgentTurnStep[];
  readonly messages: readonly AgentMessage[];
  readonly model?: string;
  readonly providerId?: string;
  readonly api?: "chat" | "responses" | "messages";
  readonly reasoning?: string;
  readonly usage?: AgentTokenUsage;
}

export interface AgentContextOptions {
  readonly compactionEnabled: boolean;
  readonly maxInputTokens: number;
  readonly reserveTokens: number;
  readonly recentTurns: number;
  readonly summaryMaxChars: number;
}

export interface AgentTurnRunnerDeps {
  readonly provider: AgentProvider;
  readonly toolGateway: AgentToolGateway;
  readonly logger?: LoggerPort;
  readonly defaultMaxToolRounds?: number;
  readonly defaultMaxRepeatedToolCalls?: number;
  readonly softRecoverAttempts?: number;
  readonly maxConcurrentToolCalls?: number;
  readonly context?: AgentContextOptions;
  readonly compactPrompt?: string;
}

export type { AgentMessage, AgentTokenUsage, AgentToolCall, AgentProvider, AgentToolGateway };
