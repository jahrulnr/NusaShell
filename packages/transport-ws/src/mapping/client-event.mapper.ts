import type { AgentTextDeltaEvent, ApplicationEvent } from "@nusashell/application";
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

    default:
      return null;
  }
}
