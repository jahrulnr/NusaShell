import type { DomainEvent } from "@nusashell/domain";
import {
  PluginStartedEvent,
  PluginStoppedEvent,
  PluginCrashedEvent,
  PluginStateChangedEvent,
  ToolCallCompletedEvent,
} from "@nusashell/domain";
import type { EventEnvelope } from "@nusashell/contracts";

export function mapDomainEvent(event: DomainEvent): EventEnvelope | null {
  const timestamp = event.occurredAt.toISOString();

  switch (event.type) {
    case "plugin.started": {
      const e = event as PluginStartedEvent;
      return {
        kind: "event",
        event: "plugin.started",
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
        payload: {
          pluginId: e.aggregateId,
          requestId: e.requestId,
          toolName: e.toolName,
          success: true,
          timestamp,
        },
      };
    }

    default:
      return null;
  }
}
