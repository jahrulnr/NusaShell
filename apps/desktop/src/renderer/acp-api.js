// ACP (Agent Control Panel) turn API — extracted from launcher.js.
// Now uses the shared turn-event helper with streamSeq gating, unified
// with the agent turn path.

import { sendRequest } from "./ws-client.js";
import { subscribeAcpTurnEvents } from "./turn-event-helper.js";

/**
 * Run an ACP turn with streamSeq-gated event subscriptions.
 *
 * @param {string} prompt
 * @param {{ traceId?: string, conversationId?: string, workspace?: string, providerId?: string, onDelta?: (delta: string, messageId?: string) => void, onReasoningDelta?: (delta: string) => void, onToolCallStart?: (p: any) => void, onToolCallEnd?: (p: any) => void, onTurnEnd?: (p: any) => void, onPermissionRequest?: (p: any) => void, onAskRequest?: (p: any) => void, onStreamGap?: (traceId: string, streamSeq: number) => void, onLog?: (level: string, message: string) => void }} options
 * @returns {Promise<any>}
 */
export async function runAcpTurn(prompt, options) {
  const providers = await window.shell.acpProviders.list();
  const selected = providers.find((p) => p.manifest.id === options.providerId);
  if (!selected) throw new Error("The ACP provider for this conversation is not configured.");

  const { disposers } = subscribeAcpTurnEvents(options);

  try {
    return await sendRequest("acp.run", {
      traceId: options.traceId,
      conversationId: options.conversationId,
      workspace: options.workspace,
      provider: { providerId: selected.manifest.id, command: selected.config.command, args: selected.config.args, authMethodId: selected.manifest.authMethodId },
      prompt,
    }, 1800000);
  } finally {
    disposers.forEach((dispose) => dispose());
  }
}

export async function cancelAcpTurn(traceId, conversationId) {
  return sendRequest("acp.cancel", { traceId, conversationId });
}

export async function getAcpSessionInfo(conversationId) {
  return sendRequest("acp.session_info", { conversationId });
}

export async function setAcpConfigOption(conversationId, configId, value) {
  return sendRequest("acp.set_config_option", { conversationId, configId, value });
}

export async function ensureAcpSession(conversationId, workspace, provider) {
  return sendRequest("acp.ensure_session", { conversationId, workspace, provider });
}

export async function answerAcpPermission(payload) {
  return sendRequest("acp.permission_answer", payload, 30000);
}

export async function answerAcpAsk(payload) {
  return sendRequest("acp.ask_answer", payload, 30000);
}
