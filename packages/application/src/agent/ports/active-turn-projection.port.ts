import type { AgentTurnStep, AgentToolExecution } from "../services/agent-turn-types.js";
import type { AgentToolCall } from "./agent-provider.port.js";

/**
 * Live mid-turn assistant draft owned by the application layer (not the
 * renderer DOM). One snapshot per conversation while a turn is in flight.
 *
 * Semantic steps (sealed reasoning / text / completed tool rounds) are the
 * durable-ish body; `streaming` + `openTools` cover the open segment that is
 * still being produced.
 */
export interface ActiveTurnOpenTool {
  readonly id: string;
  readonly name: string;
  readonly args?: Readonly<Record<string, unknown>>;
  readonly status: "running" | "ok" | "fail";
  readonly error?: string;
  readonly output?: string;
}

export interface ActiveTurnStreaming {
  readonly kind: "text" | "reasoning";
  readonly content: string;
}

export interface ActiveTurnSnapshot {
  readonly conversationId: string;
  readonly traceId: string;
  readonly status: "running" | "awaiting_input";
  readonly steps: readonly AgentTurnStep[];
  readonly streaming?: ActiveTurnStreaming;
  readonly openTools: readonly ActiveTurnOpenTool[];
  readonly model?: string;
  readonly providerId?: string;
  readonly updatedAt: string;
}

export interface ActiveTurnStartInput {
  readonly conversationId: string;
  readonly traceId: string;
  readonly model?: string;
  readonly providerId?: string;
}

/**
 * Source of truth for rehydrating the Working draft after a chat switch or
 * late client attach. Keep updates cheap: full step replace is fine (small
 * arrays); streaming patches should be in-memory only.
 */
export interface ActiveTurnProjectionPort {
  start(input: ActiveTurnStartInput): void;
  setSteps(conversationId: string, steps: readonly AgentTurnStep[], meta?: { model?: string; providerId?: string }): void;
  setStreaming(conversationId: string, streaming: ActiveTurnStreaming | null): void;
  openTool(conversationId: string, call: AgentToolCall): void;
  endTool(conversationId: string, execution: AgentToolExecution): void;
  markAwaitingInput(conversationId: string): void;
  markRunning(conversationId: string): void;
  get(conversationId: string): ActiveTurnSnapshot | undefined;
  getByTraceId(traceId: string): ActiveTurnSnapshot | undefined;
  clear(conversationId: string, traceId?: string): void;
}
