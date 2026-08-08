import type { AgentTodoItem } from "./agent-todo.js";

/** Absolute ceiling for `maxAutoContinues` when finite (matches maxToolRounds). */
export const MAX_AUTO_CONTINUES_CAP = 10_000;
export const DEFAULT_MAX_AUTO_CONTINUES = 10;

export type AutoContinueReason =
  | "continue"
  | "awaiting-background-jobs"
  | "awaiting-user"
  | "no-open-todos"
  | "max-reached"
  | "turn-not-ok"
  | "no-conversation";

export interface AutoContinueDecision {
  readonly shouldContinue: boolean;
  readonly openTodoCount: number;
  readonly continuesUsed: number;
  readonly maxAutoContinues: number;
  readonly reason: AutoContinueReason;
}

export interface AutoContinuePolicyInput {
  readonly items: readonly AgentTodoItem[];
  /** 0 = user-started turn; N > 0 = Nth auto-continue that just finished. */
  readonly autoContinueIndex: number;
  readonly maxAutoContinues: number;
  readonly turnOk: boolean;
  /** Without a conversation the chain has no todo SoT to read. */
  readonly hasConversation: boolean;
  /** Final visible text; a question means the agent is waiting for the user. */
  readonly turnText?: string;
  /** A long-running async tool owns the next useful state transition. */
  readonly hasRunningBackgroundJobs?: boolean;
}

/**
 * Pure multi-turn auto-continue policy (Codex-inspired outer loop).
 *
 * After a **successful** sealed turn, the desktop asks whether to start the
 * next turn without a user message. The decision is based only on open todos
 * and the chain budget — provider strategy, maxToolRounds, and the tool set
 * stay as-is.
 *
 * Open todos = items whose status is `pending` or `in_progress`. The chain
 * continues only when the turn succeeded, open todos remain, and the chain has
 * not exhausted `maxAutoContinues`.
 */
export function decideAutoContinue(input: AutoContinuePolicyInput): AutoContinueDecision {
  const maxAutoContinues = normalizeMaxAutoContinues(input.maxAutoContinues);
  const continuesUsed = Math.max(0, Math.floor(input.autoContinueIndex));
  const openTodoCount = countOpenTodos(input.items);

  if (!input.hasConversation) {
    return { shouldContinue: false, openTodoCount, continuesUsed, maxAutoContinues, reason: "no-conversation" };
  }
  if (!input.turnOk) {
    return { shouldContinue: false, openTodoCount, continuesUsed, maxAutoContinues, reason: "turn-not-ok" };
  }
  if (endsWithQuestion(input.turnText)) {
    return { shouldContinue: false, openTodoCount, continuesUsed, maxAutoContinues, reason: "awaiting-user" };
  }
  if (openTodoCount === 0) {
    return { shouldContinue: false, openTodoCount, continuesUsed, maxAutoContinues, reason: "no-open-todos" };
  }
  if (input.hasRunningBackgroundJobs) {
    return { shouldContinue: false, openTodoCount, continuesUsed, maxAutoContinues, reason: "awaiting-background-jobs" };
  }
  // 0 = unlimited: skip the budget check entirely.
  if (maxAutoContinues > 0 && continuesUsed >= maxAutoContinues) {
    return { shouldContinue: false, openTodoCount, continuesUsed, maxAutoContinues, reason: "max-reached" };
  }
  return { shouldContinue: true, openTodoCount, continuesUsed, maxAutoContinues, reason: "continue" };
}

function endsWithQuestion(text: string | undefined): boolean {
  return typeof text === "string" && /[?？]\s*$/.test(text.trim());
}

/**
 * Normalize the auto-continue budget.
 *
 * - `undefined` / non-finite / negative → product default (10).
 * - `0` → **unlimited** sentinel (kept as 0; `decideAutoContinue` skips the
 *   budget check). This is the opt-in escape hatch for long unattended runs.
 * - `1..CAP` → finite ceiling.
 * - `> CAP` → clamped to CAP.
 */
export function normalizeMaxAutoContinues(value: number | undefined): number {
  if (typeof value !== "number" || !Number.isFinite(value)) return DEFAULT_MAX_AUTO_CONTINUES;
  const parsed = Math.floor(value);
  if (parsed < 0) return DEFAULT_MAX_AUTO_CONTINUES;
  if (parsed === 0) return 0;
  if (parsed > MAX_AUTO_CONTINUES_CAP) return MAX_AUTO_CONTINUES_CAP;
  return parsed;
}

function countOpenTodos(items: readonly AgentTodoItem[]): number {
  let open = 0;
  for (const item of items) {
    if (item.status === "pending" || item.status === "in_progress") open += 1;
  }
  return open;
}
