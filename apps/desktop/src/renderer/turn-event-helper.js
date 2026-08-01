// Shared turn-event subscription helper for agent and ACP turns.
// Provides a unified streamSeq gate so both runAgentTurn and runAcpTurn
// get the same stale-drop / gap-detection behavior.

import { createStreamSeqGate } from "./stream-seq-gate.js";
import { onEvent } from "./ws-client.js";

/**
 * Create a gated event subscriber for a specific traceId.
 *
 * The gate drops stale/duplicate events (streamSeq <= last seen) and
 * flags gaps (streamSeq jumps by more than 1). Events without a
 * streamSeq pass through unchanged.
 *
 * @param {{ traceId: string, onStreamGap?: (traceId: string, streamSeq: number) => void, onLog?: (level: string, message: string) => void }} options
 * @returns {{ gate: (payload: any, handler: (p: any) => void) => void, onEvent: (eventType: string, handler: (p: any) => void) => () => void }}
 */
export function createTurnSubscriber(options) {
  const seqGate = createStreamSeqGate();
  const log = options.onLog ?? (() => {});

  function gate(payload, handler) {
    if (payload?.traceId !== options.traceId) return;
    const decision = seqGate.check(payload.traceId, payload.streamSeq);
    if (!decision.accept) {
      log("debug", `Dropping stale event streamSeq=${payload.streamSeq} trace=${payload.traceId}`);
      return;
    }
    if (decision.gap) {
      log("warn", `Stream gap before streamSeq=${payload.streamSeq} trace=${payload.traceId}`);
      options.onStreamGap?.(payload.traceId, payload.streamSeq);
    }
    handler(payload);
  }

  function subscribe(eventType, handler) {
    return onEvent(eventType, (payload) => gate(payload, handler));
  }

  return { gate, onEvent: subscribe };
}

/**
 * Subscribe to agent turn events with streamSeq gating.
 *
 * @param {{ traceId: string, onDelta?: (delta: string) => void, onReasoningDelta?: (delta: string) => void, onToolCallStart?: (p: any) => void, onToolCallEnd?: (p: any) => void, onContextUpdate?: (p: any) => void, onTurnStarted?: (p: any) => void, onTurnEnd?: (p: any) => void, onCancelRequested?: (p: any) => void, onTurnSuperseded?: (p: any) => void, onStreamGap?: (traceId: string, streamSeq: number) => void, onLog?: (level: string, message: string) => void }} options
 * @returns {{ disposers: (() => void)[], lifecycleDisposers: (() => void)[] }}
 */
export function subscribeAgentTurnEvents(options) {
  const sub = createTurnSubscriber(options);
  const disposers = [];
  const lifecycleDisposers = [];

  if (options.onDelta) {
    disposers.push(sub.onEvent("agent.text_delta", (p) => { if (p.delta) options.onDelta(p.delta); }));
  }
  if (options.onReasoningDelta) {
    disposers.push(sub.onEvent("agent.reasoning_delta", (p) => { if (p.delta) options.onReasoningDelta(p.delta); }));
  }
  if (options.onToolCallStart) {
    disposers.push(sub.onEvent("agent.tool_call_start", (p) => options.onToolCallStart(p)));
  }
  if (options.onToolCallEnd) {
    disposers.push(sub.onEvent("agent.tool_call_end", (p) => options.onToolCallEnd(p)));
  }
  if (options.onContextUpdate) {
    disposers.push(sub.onEvent("agent.context", (p) => options.onContextUpdate(p)));
  }
  if (options.onTurnStarted) {
    lifecycleDisposers.push(sub.onEvent("agent.turn_started", (p) => options.onTurnStarted(p)));
  }
  if (options.onTurnEnd) {
    lifecycleDisposers.push(sub.onEvent("agent.turn_end", (p) => options.onTurnEnd(p)));
  }
  if (options.onCancelRequested) {
    lifecycleDisposers.push(sub.onEvent("agent.cancel_requested", (p) => options.onCancelRequested(p)));
  }
  if (options.onTurnSuperseded) {
    lifecycleDisposers.push(sub.onEvent("agent.turn_superseded", (p) => options.onTurnSuperseded(p)));
  }

  return { disposers, lifecycleDisposers };
}

/**
 * Subscribe to ACP turn events with streamSeq gating (unified with agent turn).
 *
 * @param {{ traceId: string, onDelta?: (delta: string, messageId?: string) => void, onReasoningDelta?: (delta: string) => void, onToolCallStart?: (p: any) => void, onToolCallEnd?: (p: any) => void, onTurnEnd?: (p: any) => void, onPermissionRequest?: (p: any) => void, onAskRequest?: (p: any) => void, onStreamGap?: (traceId: string, streamSeq: number) => void, onLog?: (level: string, message: string) => void }} options
 * @returns {{ disposers: (() => void)[] }}
 */
export function subscribeAcpTurnEvents(options) {
  const sub = createTurnSubscriber(options);
  const disposers = [];

  if (options.onDelta) {
    disposers.push(sub.onEvent("acp.text_delta", (p) => { if (p.delta) options.onDelta(p.delta, p.messageId); }));
  }
  if (options.onReasoningDelta) {
    disposers.push(sub.onEvent("acp.thought_delta", (p) => { if (p.delta) options.onReasoningDelta(p.delta); }));
  }
  if (options.onToolCallStart) {
    disposers.push(sub.onEvent("acp.tool_call", (p) => options.onToolCallStart({ callId: p.call.id, name: p.call.title, args: p.call.rawInput ?? {} })));
  }
  if (options.onToolCallEnd) {
    disposers.push(sub.onEvent("acp.tool_call_update", (p) => options.onToolCallEnd({ callId: p.callId, ok: p.status === "ok", error: p.status === "fail" ? "Failed" : undefined })));
  }
  if (options.onTurnEnd) {
    disposers.push(sub.onEvent("acp.turn_end", (p) => options.onTurnEnd({ ok: p.ok, error: p.error })));
  }
  if (options.onPermissionRequest) {
    disposers.push(sub.onEvent("acp.permission_request", (p) => options.onPermissionRequest(p)));
  }
  if (options.onAskRequest) {
    disposers.push(sub.onEvent("acp.ask_request", (p) => options.onAskRequest(p)));
  }

  return { disposers };
}
