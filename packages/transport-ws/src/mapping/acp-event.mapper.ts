import type { EventEnvelope } from "@nusashell/contracts";
import type {
  AcpTextDeltaEvent,
  AcpThoughtDeltaEvent,
  AcpToolCallEvent,
  AcpToolCallUpdateEvent,
  AcpPlanEvent,
  AcpPermissionRequestEvent,
  AcpAskRequestEvent,
  AcpTurnEndEvent,
  AcpSessionStateEvent,
  ApplicationEvent,
} from "@nusashell/application";
import { redactString, redactValue } from "./redact.js";

/**
 * Maps ACP-domain events (text/thought deltas, tool calls, plan, permission/
 * ask requests, turn end, session state) to WS event envelopes.
 */
export function mapAcpEvent(
  event: ApplicationEvent,
  sequence: number,
  timestamp: string,
): EventEnvelope | null {
  switch (event.type) {
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
        payload: { traceId: e.aggregateId, call: redactValue({ ...e.call }), timestamp },
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
        payload: { traceId: e.aggregateId, steps: [...e.steps], timestamp },
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
