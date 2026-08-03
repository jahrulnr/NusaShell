// Agent turn API — extracted from launcher.js.
// Uses the shared turn-event helper for streamSeq-gated subscriptions.

import { sendRequest } from "./ws-client.js";
import { subscribeAgentTurnEvents } from "./turn-event-helper.js";

/**
 * Run an agent turn with streamSeq-gated event subscriptions.
 *
 * @param {readonly {role: string, content: any}[]} messages
 * @param {{ traceId?: string, workspace?: string, resume?: boolean, supersedeTraceId?: string, onDelta?: (delta: string) => void, onReasoningDelta?: (delta: string) => void, onToolCallStart?: (p: any) => void, onToolCallEnd?: (p: any) => void, onAskRequest?: (p: any) => void, onContextUpdate?: (p: any) => void, onTurnStarted?: (p: any) => void, onTurnEnd?: (p: any) => void, onCancelRequested?: (p: any) => void, onTurnSuperseded?: (p: any) => void, onStreamGap?: (traceId: string, streamSeq: number) => void, onLog?: (level: string, message: string) => void }} options
 * @param {{ models: any[], activeModelKey: string, effort: string, userPrompt?: string }} aiSettings
 * @returns {Promise<any>}
 */
export async function runAgentTurn(messages, options, aiSettings) {
  const selected = aiSettings.models.find((model) => model.key === aiSettings.activeModelKey);
  if (!selected) throw new Error("Choose an imported AI model before sending a turn.");

  const { disposers, lifecycleDisposers } = subscribeAgentTurnEvents(options);

  try {
    return await sendRequest("agent.run", {
      messages,
      pluginIds: [],
      providerId: selected.providerId,
      model: selected.id,
      effort: aiSettings.effort,
      userPrompt: aiSettings.userPrompt,
      ...(options.workspace ? { workspace: options.workspace } : {}),
      ...(options.resume ? { resume: true } : {}),
      ...(options.supersedeTraceId ? { supersedeTraceId: options.supersedeTraceId } : {}),
      ...(options.conversationId ? { conversationId: options.conversationId } : {}),
      modelCapabilities: {
        contextWindow: selected.contextWindow,
        maxOutput: selected.maxOutput,
        inputModes: selected.inputModes,
        outputModes: selected.outputModes,
        supportedEfforts: selected.supportedEfforts,
        defaultEffort: selected.defaultEffort,
        reasoningSupported: selected.reasoningSupported,
        reasoningMandatory: selected.reasoningMandatory,
        reasoningSupportsMaxTokens: selected.reasoningSupportsMaxTokens,
        supportsTools: selected.supportsTools,
        supportsVision: selected.supportsVision,
      },
      ...(options.traceId ? { traceId: options.traceId } : {}),
    }, 1800000);
  } finally {
    disposers.forEach((dispose) => dispose());
    // Lifecycle handlers stay registered briefly after the run settles so a
    // turn_end/cancel_requested event published asynchronously after the
    // agent.run rejection still reaches the UI (WS delivery order is not
    // guaranteed; streamSeq + the 2s UI wait make ordering best-effort).
    setTimeout(() => lifecycleDisposers.forEach((dispose) => dispose()), 2500);
  }
}

export async function cancelAgentTurn(traceId) {
  return sendRequest("agent.cancel", { traceId });
}

export async function answerAskQuestion(payload) {
  return sendRequest("agent.ask_answer", payload, 30000);
}
