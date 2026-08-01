import os from "node:os";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { AcpClientPort, AcpClientSink, AcpProviderDescriptor } from "../../ports/acp-client.port.js";
import type { ProbeAcpProviderCommand, ProbeAcpProviderResult } from "./probe-acp-provider.command.js";

const NOOP_SINK: AcpClientSink = {
  publish: () => {},
  requestPermission: async () => ({ optionId: "deny" }),
  askQuestion: async () => ({ text: "" }),
};

/**
 * One-shot connectivity probe for an ACP provider.
 *
 * Spawn → initialize → optional authenticate (soft-fail) → session/new → close.
 * Does NOT send a prompt, so no permissions are expected. Used by the AI
 * Providers → ACP Agents "Connect" button to verify auth before a thread opens.
 */
export class ProbeAcpProviderHandler implements CommandHandler<ProbeAcpProviderCommand, ProbeAcpProviderResult> {
  constructor(private readonly client: AcpClientPort) {}

  async handle(command: ProbeAcpProviderCommand): Promise<ProbeAcpProviderResult> {
    const provider: AcpProviderDescriptor = {
      providerId: command.provider.providerId,
      command: command.provider.command,
      args: command.provider.args,
      ...(command.provider.authMethodId ? { authMethodId: command.provider.authMethodId } : {}),
      ...(command.provider.env ? { env: command.provider.env } : {}),
    };
    const conversationId = `probe-${provider.providerId}-${Date.now()}`;
    const cwd = os.tmpdir();
    try {
      await this.client.startSession(conversationId, provider, cwd, NOOP_SINK);
      await this.client.closeSession(conversationId).catch(() => {});
      return { ok: true };
    } catch (error) {
      await this.client.closeSession(conversationId).catch(() => {});
      return { ok: false, error: error instanceof Error ? error.message : String(error) };
    }
  }
}
