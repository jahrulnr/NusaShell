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
import type { AutoContinueDecision } from "./auto-continue-policy.js";

export const MAX_REPEATED_TOOL_CALLS = 50;
export const DEFAULT_MAX_TOOL_ROUNDS = 50;
/** Absolute ceiling for settings/env/API validation (complex agentic runs). */
export const MAX_TOOL_ROUNDS_CAP = 10_000;
export const DEFAULT_SOFT_RECOVER_ATTEMPTS = 1;
export const MAX_SOFT_RECOVER_ATTEMPTS = 3;
export const DEFAULT_MAX_CONCURRENT_TOOL_CALLS = 8;
export const MAX_CONCURRENT_TOOL_CALLS_CAP = 32;

/**
 * Tools that must run alone, in order (interactive barriers).
 * `ask_question` blocks the turn for user input and cannot overlap siblings.
 * `mcp_register` / `mcp_unregister` also wait on nested confirmation asks.
 */
export const BARRIER_TOOLS: ReadonlySet<string> = new Set([
  "ask_question",
  "mcp_register",
  "mcp_unregister",
  "async_wait",
]);

export interface RunAgentTurnInput {
  readonly messages: readonly AgentMessage[];
  readonly pluginIds: readonly string[];
  readonly maxToolRounds?: number;
  readonly model?: string;
  readonly effort?: ReasoningEffort;
  readonly modelCapabilities?: AgentModelCapabilities;
  readonly traceId?: string;
  readonly interactive?: boolean;
  /** Conversation workspace bound for tool I/O / subagent cwd. */
  readonly workspace?: string;
  readonly signal?: AbortSignal;
  readonly onTextDelta?: (delta: string) => void;
  readonly onReasoningDelta?: (delta: string) => void;
  readonly onToolCallStart?: (call: AgentToolCall) => void;
  readonly onToolCallEnd?: (execution: AgentToolExecution) => void;
  readonly onContextUpdate?: (update: AgentContextUpdate) => void;
  /**
   * Fired whenever the sealed step list grows (reasoning / text / tool_calls).
   * Used by ActiveTurnProjection — not every token.
   */
  readonly onStepsChanged?: (steps: readonly AgentTurnStep[]) => void;
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
  /**
   * Canonical typed tool result (dual-rep). Populated by the execution policy
   * after migration; legacy `ok`/`result`/`error` remain derived for consumers
   * that have not yet switched.
   */
  readonly toolResult?: import("./agent-tool-result.js").AgentToolResult;
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
  /**
   * Outer multi-turn auto-continue decision. Attached only on a successful
   * complete turn when a conversation is bound and a todo port is configured;
   * omitted on failed/cancelled paths so the desktop never chains those.
   */
  readonly autoContinue?: AutoContinueDecision;
}

export interface AgentCompactionCheckpoint {
  readonly summary: string;
  readonly compactedMessageCount: number;
  readonly estimatedInputTokens: number;
  readonly via: "provider" | "extractive";
  /**
   * Codex-aligned retained user message texts (chronological) packed into the
   * replacement history. Present when the compactor produced a memento
   * replacement; absent for legacy checkpoints (migration falls back to
   * `compactedMessageCount` slice).
   */
  readonly retainedUserMessages?: readonly string[];
}

/**
 * Mid-turn progress snapshot attached to ApplicationError `details.partial`
 * when a turn fails after tool work has already accumulated (provider 4xx/5xx
 * after soft recover, allowlist rejection, listTools failure, etc.).
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
