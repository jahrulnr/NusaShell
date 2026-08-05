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
      ...(options.autoContinueIndex ? { autoContinueIndex: options.autoContinueIndex } : {}),
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

/**
 * Fetch the application-layer mid-turn projection for a conversation.
 * Used to rehydrate the Working draft after a chat switch.
 */
export async function getActiveTurn(conversationId) {
  return sendRequest("agent.get_active_turn", { conversationId }, 15000);
}

/**
 * Replace the conversation todo list (user-initiated from the strip UI).
 */
export async function setTodos(conversationId, items) {
  return sendRequest("agent.todos_set", { conversationId, items }, 15000);
}

/**
 * Delete specific todo items by id (user-initiated from the strip UI).
 */
export async function deleteTodos(conversationId, ids) {
  return sendRequest("agent.todos_delete", { conversationId, ids }, 15000);
}

/** Fetch the current checklist so the strip can recover after a late mount. */
export async function getTodos(conversationId) {
  const result = await sendRequest("agent.todos_get", { conversationId }, 15000);
  return result?.items ?? [];
}

/**
 * List active async tool jobs for a conversation (rehydrate on chat switch).
 */
export async function listToolJobs(conversationId) {
  return sendRequest("agent.tool_job_list", { conversationId }, 15000);
}

/**
 * Kill a background tool job by handleId (user-initiated from the job card).
 */
export async function killToolJob(handleId) {
  return sendRequest("agent.tool_job_kill", { handleId }, 15000);
}
