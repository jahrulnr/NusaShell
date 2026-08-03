/**
 * Shared assistant message builder.
 *
 * Constructs the durable `AgentConversationMessage` for an assistant turn
 * from an `AgentTurnResult` (or partial). Used by the desktop main process
 * to seal the reply off the renderer critical path, and by the renderer to
 * seal the streaming UI after the turn resolves.
 *
 * Mirrors the clamping logic in `agent-conversation-ui.js` so persisted
 * messages stay within the store validator's size caps.
 */
import type { AgentTurnResult, AgentTurnPartial } from "@nusashell/application";
import type {
  AgentConversationMessage,
  AgentConversationToolCall,
  AgentConversationStep,
} from "./agent-conversation-contract.js";

const TOOL_ARGS_MAX_CHARS = 8_000;
const TOOL_OUTPUT_MAX_CHARS = 12_000;
const TOOL_ERROR_MAX_CHARS = 4_000;

function clampText(value: unknown, maxChars: number): string {
  const text = typeof value === "string" ? value : String(value ?? "");
  return text.length > maxChars ? `${text.slice(0, maxChars)}` : text;
}

function formatToolOutput(value: unknown): string {
  if (value === undefined || value === null) return "";
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

/**
 * Build a durable tool call from an `AgentToolExecution`-like object.
 * Always includes `args` (defaulting to `{}`) so older transcripts without
 * args still validate against the schema.
 */
export function buildToolCall(call: {
  id: string;
  name: string;
  ok?: boolean;
  error?: string;
  args?: Record<string, unknown>;
  output?: string;
  result?: unknown;
}): AgentConversationToolCall {
  const rawArgs = call.args && typeof call.args === "object" && !Array.isArray(call.args) ? call.args : undefined;
  let safeArgs: Record<string, unknown> = {};
  if (rawArgs && Object.keys(rawArgs).length > 0) {
    try {
      const encoded = JSON.stringify(rawArgs);
      if (encoded.length <= TOOL_ARGS_MAX_CHARS) {
        safeArgs = rawArgs as Record<string, unknown>;
      } else {
        let budget = TOOL_ARGS_MAX_CHARS - JSON.stringify({ _truncated: "" }).length;
        let truncated: Record<string, unknown> = { _truncated: clampText(encoded, budget) };
        for (let attempt = 0; attempt < 3; attempt++) {
          truncated = { _truncated: clampText(encoded, budget) };
          const overflow = JSON.stringify(truncated).length - TOOL_ARGS_MAX_CHARS;
          if (overflow <= 0) break;
          budget -= overflow;
        }
        if (JSON.stringify(truncated).length <= TOOL_ARGS_MAX_CHARS) {
          safeArgs = truncated;
        }
      }
    } catch {
      // keep safeArgs as {}
    }
  }

  const output =
    call.output !== undefined
      ? clampText(call.output, TOOL_OUTPUT_MAX_CHARS)
      : call.error
        ? clampText(call.error, TOOL_OUTPUT_MAX_CHARS)
        : call.result !== undefined
          ? clampText(formatToolOutput(call.result), TOOL_OUTPUT_MAX_CHARS)
          : undefined;

  return {
    id: call.id,
    name: call.name,
    ok: call.ok !== false,
    ...(call.error ? { error: clampText(call.error, TOOL_ERROR_MAX_CHARS) } : {}),
    args: safeArgs,
    ...(output ? { output } : {}),
  };
}

function buildSteps(steps: readonly { type: string; content?: string; calls?: readonly any[]; model?: string; providerId?: string }[] | undefined): AgentConversationStep[] | undefined {
  if (!Array.isArray(steps) || steps.length === 0) return undefined;
  const result: AgentConversationStep[] = [];
  for (const step of steps) {
    if (step.type === "text" && typeof step.content === "string") {
      result.push({ type: "text", content: step.content });
    } else if (step.type === "reasoning" && typeof step.content === "string") {
      result.push({ type: "reasoning", content: step.content });
    } else if (step.type === "tool_calls" && Array.isArray(step.calls)) {
      const calls = step.calls.map(buildToolCall);
      if (calls.length > 0) result.push({ type: "tool_calls", calls });
    }
  }
  return result.length > 0 ? result : undefined;
}

/**
 * Build the durable assistant message from a completed turn result.
 */
export function buildAssistantMessage(result: AgentTurnResult): AgentConversationMessage {
  const toolCalls = Array.isArray(result.toolCalls) && result.toolCalls.length > 0
    ? result.toolCalls.map(buildToolCall)
    : undefined;
  const steps = buildSteps(result.steps as any);
  return {
    role: "assistant",
    content: result.text,
    traceId: result.traceId,
    ...(result.model !== undefined ? { model: result.model } : {}),
    rounds: result.rounds,
    ...(result.reasoning ? { reasoning: result.reasoning } : {}),
    ...(toolCalls ? { toolCalls } : {}),
    ...(steps ? { steps } : {}),
  };
}

/**
 * Build the interrupted assistant message from a partial turn result.
 */
export function buildInterruptedMessage(partial: AgentTurnPartial): AgentConversationMessage {
  const toolCalls = Array.isArray(partial.toolCalls) && partial.toolCalls.length > 0
    ? partial.toolCalls.map(buildToolCall)
    : undefined;
  const steps = buildSteps(partial.steps as any);
  return {
    role: "assistant",
    content: `Turn interrupted after ${partial.rounds} tool round${partial.rounds === 1 ? "" : "s"}.`,
    status: "interrupted",
    traceId: partial.traceId,
    ...(partial.model !== undefined ? { model: partial.model } : {}),
    rounds: partial.rounds,
    ...(partial.reasoning ? { reasoning: partial.reasoning } : {}),
    ...(toolCalls ? { toolCalls } : {}),
    ...(steps ? { steps } : {}),
  };
}
