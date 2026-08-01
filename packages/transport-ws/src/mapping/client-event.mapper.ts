import type { AgentReasoningDeltaEvent, AgentTextDeltaEvent, AgentToolCallEndEvent, AgentToolCallStartEvent, AgentContextUpdateEvent, AgentTurnStartedEvent, AgentTurnEndEvent, AgentTurnSupersededEvent, AgentCancelRequestedEvent, AgentLearningUpdatedEvent, JobCompletedEvent, JobFailedEvent, ApplicationEvent, AcpTextDeltaEvent, AcpThoughtDeltaEvent, AcpToolCallEvent, AcpToolCallUpdateEvent, AcpPlanEvent, AcpPermissionRequestEvent, AcpAskRequestEvent, AcpTurnEndEvent, AcpSessionStateEvent } from "@nusashell/application";
import { redactArgs, redactString, redactValue } from "./redact.js";
import {
  PluginInstalledEvent,
  PluginUninstalledEvent,
  PluginStartedEvent,
  PluginStoppedEvent,
  PluginCrashedEvent,
  PluginStateChangedEvent,
  ToolCallCompletedEvent,
} from "@nusashell/domain";
import type { EventEnvelope } from "@nusashell/contracts";

export function mapDomainEvent(event: ApplicationEvent, sequence: number): EventEnvelope | null {
  const envelope = mapDomainEventInner(event, sequence);
  if (envelope && event.streamSeq !== undefined) {
    return {
      ...envelope,
      payload: { ...(envelope.payload as Record<string, unknown>), streamSeq: event.streamSeq },
    };
  }
  return envelope;
}

function mapDomainEventInner(event: ApplicationEvent, sequence: number): EventEnvelope | null {
  const timestamp = event.occurredAt.toISOString();

  switch (event.type) {
    case "plugin.installed": {
      const e = event as PluginInstalledEvent;
      return {
        kind: "event",
        event: "plugin.installed",
        sequence,
        payload: {
          pluginId: e.aggregateId,
          version: e.version,
          timestamp,
        },
      };
    }

    case "plugin.uninstalled": {
      const e = event as PluginUninstalledEvent;
      return {
        kind: "event",
        event: "plugin.uninstalled",
        sequence,
        payload: {
          pluginId: e.aggregateId,
          timestamp,
        },
      };
    }

    case "plugin.started": {
      const e = event as PluginStartedEvent;
      return {
        kind: "event",
        event: "plugin.started",
        sequence,
        payload: {
          pluginId: e.aggregateId,
          state: "running",
          pid: e.pid ?? 0,
          timestamp,
        },
      };
    }

    case "plugin.stopped": {
      const e = event as PluginStoppedEvent;
      return {
        kind: "event",
        event: "plugin.stopped",
        sequence,
        payload: {
          pluginId: e.aggregateId,
          state: "idle",
          timestamp,
        },
      };
    }

    case "plugin.crashed": {
      const e = event as PluginCrashedEvent;
      return {
        kind: "event",
        event: "plugin.crashed",
        sequence,
        payload: {
          pluginId: e.aggregateId,
          state: "crashed",
          exitCode: -1,
          timestamp,
        },
      };
    }

    case "plugin.state_changed": {
      const e = event as PluginStateChangedEvent;
      return {
        kind: "event",
        event: "plugin.state_changed",
        sequence,
        payload: {
          pluginId: e.aggregateId,
          oldState: e.from,
          newState: e.to,
          timestamp,
        },
      };
    }

    case "tool.call_completed": {
      const e = event as ToolCallCompletedEvent;
      return {
        kind: "event",
        event: "tool.call_completed",
        sequence,
        payload: {
          pluginId: e.aggregateId,
          requestId: e.requestId,
          toolName: e.toolName,
          success: true,
          timestamp,
        },
      };
    }

    case "agent.text_delta": {
      const e = event as AgentTextDeltaEvent;
      return {
        kind: "event",
        event: "agent.text_delta",
        sequence,
        payload: {
          traceId: e.aggregateId,
          delta: e.delta,
          timestamp,
        },
      };
    }

    case "agent.reasoning_delta": {
      const e = event as AgentReasoningDeltaEvent;
      return {
        kind: "event",
        event: "agent.reasoning_delta",
        sequence,
        payload: {
          traceId: e.aggregateId,
          delta: e.delta,
          timestamp,
        },
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
        payload: {
          traceId: e.aggregateId,
          reason: e.reason,
          timestamp,
        },
      };
    }

    case "agent.turn_superseded": {
      const e = event as AgentTurnSupersededEvent;
      return {
        kind: "event",
        event: "agent.turn_superseded",
        sequence,
        payload: {
          traceId: e.aggregateId,
          byTraceId: e.byTraceId,
          timestamp,
        },
      };
    }

    case "agent.cancel_requested": {
      const e = event as AgentCancelRequestedEvent;
      return {
        kind: "event",
        event: "agent.cancel_requested",
        sequence,
        payload: {
          traceId: e.aggregateId,
          timestamp,
        },
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

    case "job.completed": {
      const e = event as JobCompletedEvent;
      return {
        kind: "event",
        event: "job.completed",
        sequence,
        payload: {
          jobId: e.jobId,
          name: e.name,
          summary: e.summary,
          timestamp,
        },
      };
    }

    case "job.failed": {
      const e = event as JobFailedEvent;
      return {
        kind: "event",
        event: "job.failed",
        sequence,
        payload: {
          jobId: e.jobId,
          name: e.name,
          error: e.error,
          timestamp,
        },
      };
    }

    case "acp.text_delta": {
      const e = event as AcpTextDeltaEvent;
      return {
        kind: "event",
        event: "acp.text_delta",
        sequence,
        payload: { traceId: e.aggregateId, delta: e.delta, messageId: e.messageId, timestamp },
      };
    }

    case "acp.thought_delta": {
      const e = event as AcpThoughtDeltaEvent;
      return {
        kind: "event",
        event: "acp.thought_delta",
        sequence,
        payload: { traceId: e.aggregateId, delta: e.delta, timestamp },
      };
    }

    case "acp.tool_call": {
      const e = event as AcpToolCallEvent;
      return {
        kind: "event",
        event: "acp.tool_call",
        sequence,
        payload: {
          traceId: e.aggregateId,
          call: redactValue({ ...e.call }),
          timestamp,
        },
      };
    }

    case "acp.tool_call_update": {
      const e = event as AcpToolCallUpdateEvent;
      return {
        kind: "event",
        event: "acp.tool_call_update",
        sequence,
        payload: {
          traceId: e.aggregateId,
          callId: e.callId,
          status: e.status,
          ...(e.summary !== undefined ? { summary: redactString(e.summary) } : {}),
          timestamp,
        },
      };
    }

    case "acp.plan": {
      const e = event as AcpPlanEvent;
      return {
        kind: "event",
        event: "acp.plan",
        sequence,
        payload: {
          traceId: e.aggregateId,
          steps: [...e.steps],
          timestamp,
        },
      };
    }

    case "acp.permission_request": {
      const e = event as AcpPermissionRequestEvent;
      return {
        kind: "event",
        event: "acp.permission_request",
        sequence,
        payload: {
          traceId: e.aggregateId,
          requestId: e.requestId,
          toolTitle: e.toolTitle,
          ...(e.detail !== undefined ? { detail: e.detail } : {}),
          options: [...e.options],
          timestamp,
        },
      };
    }

    case "acp.ask_request": {
      const e = event as AcpAskRequestEvent;
      return {
        kind: "event",
        event: "acp.ask_request",
        sequence,
        payload: {
          traceId: e.aggregateId,
          requestId: e.requestId,
          question: e.question,
          ...(e.options !== undefined ? { options: [...e.options] } : {}),
          ...(e.multiSelect !== undefined ? { multiSelect: e.multiSelect } : {}),
          ...(e.allowFreeText !== undefined ? { allowFreeText: e.allowFreeText } : {}),
          timestamp,
        },
      };
    }

    case "acp.turn_end": {
      const e = event as AcpTurnEndEvent;
      return {
        kind: "event",
        event: "acp.turn_end",
        sequence,
        payload: {
          traceId: e.aggregateId,
          ok: e.ok,
          ...(e.error !== undefined ? { error: redactString(e.error) } : {}),
          timestamp,
        },
      };
    }

    case "acp.session_state": {
      const e = event as AcpSessionStateEvent;
      return {
        kind: "event",
        event: "acp.session_state",
        sequence,
        payload: {
          traceId: e.aggregateId,
          conversationId: e.conversationId,
          state: e.state,
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
