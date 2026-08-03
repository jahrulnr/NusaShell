// Subagent event helper — subscribes to subagent.run_started / subagent.run_ended
// and routes ACP streaming events for the runId to the side pane.

import { onEvent } from "./ws-client.js";
import { subscribeAcpTurnEvents } from "./turn-event-helper.js";

/**
 * Subscribe to subagent lifecycle + ACP streaming events.
 *
 * @param {{ onRunStarted?: (p: any) => void, onRunEnded?: (p: any) => void, onDelta?: (delta: string) => void, onReasoningDelta?: (delta: string) => void, onToolCallStart?: (p: any) => void, onToolCallEnd?: (p: any) => void, onPlan?: (steps: any[]) => void, onSessionState?: (p: any) => void, onLog?: (level: string, message: string) => void }} handlers
 * @returns {() => void} disposer
 */
export function subscribeSubagentEvents(handlers) {
  const disposers = [];

  disposers.push(onEvent("subagent.run_started", (p) => {
    handlers.onRunStarted?.(p);
  }));

  disposers.push(onEvent("subagent.run_ended", (p) => {
    handlers.onRunEnded?.(p);
  }));

  return () => disposers.forEach((d) => d());
}

/**
 * Subscribe to ACP streaming events for a specific subagent runId.
 * Returns a disposer to clean up when the run ends.
 *
 * @param {string} runId
 * @param {{ onDelta?: (delta: string) => void, onReasoningDelta?: (delta: string) => void, onToolCallStart?: (p: any) => void, onToolCallEnd?: (p: any) => void, onLog?: (level: string, message: string) => void }} handlers
 * @returns {() => void}
 */
export function subscribeSubagentStream(runId, handlers) {
  const acpHandlers = {
    traceId: runId,
    onDelta: handlers.onDelta,
    onReasoningDelta: handlers.onReasoningDelta,
    onToolCallStart: handlers.onToolCallStart,
    onToolCallEnd: handlers.onToolCallEnd,
    onPermissionRequest: handlers.onPermissionRequest,
    onAskRequest: handlers.onAskRequest,
    onLog: handlers.onLog,
  };

  // Also subscribe to plan and session_state events (not in subscribeAcpTurnEvents)
  const extraDisposers = [];
  extraDisposers.push(onEvent("acp.plan", (p) => {
    if (p.traceId !== runId) return;
    handlers.onPlan?.(p.steps);
  }));
  extraDisposers.push(onEvent("acp.session_state", (p) => {
    if (p.traceId !== runId) return;
    handlers.onSessionState?.(p);
  }));

  const { disposers } = subscribeAcpTurnEvents(acpHandlers);

  return () => {
    disposers.forEach((d) => d());
    extraDisposers.forEach((d) => d());
  };
}
