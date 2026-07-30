import type { AgentReasoningDeltaEvent, AgentTextDeltaEvent, AgentToolCallEndEvent, AgentToolCallStartEvent, AgentContextUpdateEvent, ApplicationEvent } from "@nusashell/application";
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
          pid: 0,
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
