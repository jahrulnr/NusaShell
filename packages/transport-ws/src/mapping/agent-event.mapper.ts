import type { EventEnvelope } from "@nusashell/contracts";
import type {
  AgentAskRequestEvent,
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
  AgentTodoUpdatedEvent,
  AgentToolJobStartedEvent,
  AgentToolJobUpdateEvent,
  AgentToolJobEndedEvent,
  SubagentRunStartedEvent,
  SubagentRunEndedEvent,
  ApplicationEvent,
} from "@nusashell/application";

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
          ...(hasArgs(e.call.args) ? { args: e.call.args } : {}),
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
          ...(e.execution.error ? { error: e.execution.error } : {}),
          ...(hasArgs(e.execution.args) ? { args: e.execution.args } : {}),
          ...(output ? { output } : {}),
          timestamp,
        },
      };
    }
    case "agent.ask_request": {
      const e = event as AgentAskRequestEvent;
      return {
        kind: "event",
        event: "agent.ask_request",
        sequence,
        payload: {
          traceId: e.traceId,
          callId: e.callId,
          question: e.question,
          options: e.options.map((option) => ({
            id: option.id,
            label: option.label,
            ...(option.description ? { description: option.description } : {}),
            ...(option.default ? { default: true } : {}),
            ...(option.icon ? { icon: option.icon } : {}),
            ...(option.image ? { image: option.image } : {}),
          })),
          allowFreeText: e.allowFreeText,
          multiSelect: e.multiSelect,
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
    case "subagent.run_started": {
      const e = event as SubagentRunStartedEvent;
      return {
        kind: "event",
        event: "subagent.run_started",
        sequence,
        payload: {
          runId: e.runId,
          conversationId: e.conversationId,
          providerId: e.providerId,
          prompt: e.prompt,
          ...(e.title ? { title: e.title } : {}),
          ...(e.parentConversationId ? { parentConversationId: e.parentConversationId } : {}),
          ...(e.parentTraceId ? { parentTraceId: e.parentTraceId } : {}),
          timestamp,
        },
      };
    }
    case "subagent.run_ended": {
      const e = event as SubagentRunEndedEvent;
      return {
        kind: "event",
        event: "subagent.run_ended",
        sequence,
        payload: {
          runId: e.runId,
          conversationId: e.conversationId,
          providerId: e.providerId,
          ok: e.ok,
          ...(e.summary ? { summary: e.summary } : {}),
          ...(e.error ? { error: e.error } : {}),
          timestamp,
        },
      };
    }
    case "agent.todo_updated": {
      const e = event as AgentTodoUpdatedEvent;
      return {
        kind: "event",
        event: "agent.todo_updated",
        sequence,
        payload: {
          conversationId: e.conversationId,
          items: e.items.map((item) => ({
            id: item.id,
            content: item.content,
            status: item.status,
          })),
          timestamp,
        },
      };
    }
    case "agent.tool_job_started": {
      const e = event as AgentToolJobStartedEvent;
      return {
        kind: "event",
        event: "agent.tool_job_started",
        sequence,
        payload: {
          handleId: e.handleId,
          conversationId: e.conversationId,
          kind: e.kind,
          toolName: e.toolName,
          argsSummary: e.argsSummary,
          ...(e.pluginId ? { pluginId: e.pluginId } : {}),
          ...(e.traceId ? { traceId: e.traceId } : {}),
          timestamp,
        },
      };
    }
    case "agent.tool_job_update": {
      const e = event as AgentToolJobUpdateEvent;
      return {
        kind: "event",
        event: "agent.tool_job_update",
        sequence,
        payload: {
          handleId: e.handleId,
          conversationId: e.conversationId,
          status: e.status,
          tail: e.tail,
          bytes: e.bytes,
          streamSeq: e.streamSeq,
          timestamp,
        },
      };
    }
    case "agent.tool_job_ended": {
      const e = event as AgentToolJobEndedEvent;
      return {
        kind: "event",
        event: "agent.tool_job_ended",
        sequence,
        payload: {
          handleId: e.handleId,
          conversationId: e.conversationId,
          ok: e.ok,
          reason: e.reason,
          ...(e.error !== undefined ? { error: e.error } : {}),
          ...(e.output !== undefined ? { output: e.output } : {}),
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
