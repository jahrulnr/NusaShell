import type { EventEnvelope } from "@nusashell/contracts";
import type {
  AgentReasoningDeltaEvent,
  AgentTextDeltaEvent,
  AgentToolCallEndEvent,
  AgentToolCallStartEvent,
  AgentContextUpdateEvent,
  AgentTurnStartedEvent,
  AgentTurnEndEvent,
  AgentTurnSupersededEvent,
  AgentCancelRequestedEvent,
  AgentLearningUpdatedEvent,
  ApplicationEvent,
} from "@nusashell/application";
import { redactArgs, redactString } from "./redact.js";

/**
 * Maps agent-domain events (text/reasoning deltas, tool call start/end, context
 * updates, turn lifecycle, cancellation, learning updates) to WS envelopes.
 */
export function mapAgentEvent(
  event: ApplicationEvent,
  sequence: number,
  timestamp: string,
): EventEnvelope | null {
  switch (event.type) {
    case "agent.text_delta": {
      const e = event as AgentTextDeltaEvent;
      return {
        kind: "event",
        event: "agent.text_delta",
        sequence,
        payload: { traceId: e.aggregateId, delta: e.delta, timestamp },
      };
    }
    case "agent.reasoning_delta": {
      const e = event as AgentReasoningDeltaEvent;
      return {
        kind: "event",
        event: "agent.reasoning_delta",
        sequence,
        payload: { traceId: e.aggregateId, delta: e.delta, timestamp },
      };
    }
    case "agent.tool_call_start": {
      const e = event as AgentToolCallStartEvent;
      return {
        kind: "event",
        event: "agent.tool_call_start",
        sequence,
        payload: {
          traceId: e.aggregateId,
          callId: e.call.id,
          name: e.call.name,
          ...(hasArgs(e.call.args) ? { args: redactArgs(e.call.args) } : {}),
          timestamp,
        },
      };
    }
    case "agent.tool_call_end": {
      const e = event as AgentToolCallEndEvent;
      const output = formatToolEventOutput(e.execution);
      return {
        kind: "event",
        event: "agent.tool_call_end",
        sequence,
        payload: {
          traceId: e.aggregateId,
          callId: e.execution.id,
          name: e.execution.name,
          ok: e.execution.ok,
          ...(e.execution.error ? { error: redactString(e.execution.error) } : {}),
          ...(hasArgs(e.execution.args) ? { args: redactArgs(e.execution.args) } : {}),
          ...(output ? { output: redactString(output) } : {}),
          timestamp,
        },
      };
    }
    case "agent.context": {
      const e = event as AgentContextUpdateEvent;
      return {
        kind: "event",
        event: "agent.context",
        sequence,
        payload: {
          traceId: e.aggregateId,
          estimatedTokens: e.estimatedTokens,
          ...(e.usage ? {
            inputTokens: e.usage.inputTokens,
            outputTokens: e.usage.outputTokens,
          } : {}),
          timestamp,
        },
      };
    }
    case "agent.turn_started": {
      const e = event as AgentTurnStartedEvent;
      return {
        kind: "event",
        event: "agent.turn_started",
        sequence,
        payload: {
          traceId: e.aggregateId,
          ...(e.conversationId !== undefined ? { conversationId: e.conversationId } : {}),
          timestamp,
        },
      };
    }
    case "agent.turn_end": {
      const e = event as AgentTurnEndEvent;
      return {
        kind: "event",
        event: "agent.turn_end",
        sequence,
        payload: { traceId: e.aggregateId, reason: e.reason, timestamp },
      };
    }
    case "agent.turn_superseded": {
      const e = event as AgentTurnSupersededEvent;
      return {
        kind: "event",
        event: "agent.turn_superseded",
        sequence,
        payload: { traceId: e.aggregateId, byTraceId: e.byTraceId, timestamp },
      };
    }
    case "agent.cancel_requested": {
      const e = event as AgentCancelRequestedEvent;
      return {
        kind: "event",
        event: "agent.cancel_requested",
        sequence,
        payload: { traceId: e.aggregateId, timestamp },
      };
    }
    case "agent.learning_updated": {
      const e = event as AgentLearningUpdatedEvent;
      return {
        kind: "event",
        event: "agent.learning_updated",
        sequence,
        payload: {
          reviewTraceId: e.reviewTraceId,
          kinds: [...e.kinds],
          summary: e.summary,
          timestamp,
        },
      };
    }
    default:
      return null;
  }
}

function hasArgs(args: Readonly<Record<string, unknown>> | undefined): args is Readonly<Record<string, unknown>> {
  return Boolean(args && Object.keys(args).length > 0);
}

function formatToolEventOutput(execution: AgentToolCallEndEvent["execution"]): string | undefined {
  if (execution.error) return clampToolText(execution.error, 12_000);
  if (execution.result === undefined) return undefined;
  if (typeof execution.result === "string") return clampToolText(execution.result, 12_000);
  try {
    return clampToolText(JSON.stringify(execution.result, null, 2), 12_000);
  } catch {
    return clampToolText(String(execution.result), 12_000);
  }
}

function clampToolText(value: string, maxChars: number): string {
  return value.length <= maxChars ? value : `${value.slice(0, maxChars)}\n…`;
}
