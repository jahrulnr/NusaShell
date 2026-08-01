import type { AcpExtensionHandled, AcpProviderExtension } from "./acp-provider-extension.js";

/**
 * Codex ACP extension — no-op for MVP.
 *
 * Codex (`@agentclientprotocol/codex-acp`) speaks the standard ACP permission
 * path, so the core client handles it without vendor methods. This stub exists
 * to document where Codex-specific handling would land later:
 *  - `_meta.codex.subagent` mapping
 *  - `session_info_update` / `available_commands_update` surfacing
 *  - slash-command introspection
 *
 * Spawn-env defaults (`NO_BROWSER`, `INITIAL_AGENT_MODE`) are declared on the
 * provider manifest, not here, so users can override them via Configure.
 */
export class CodexAcpExtension implements AcpProviderExtension {
  matches(providerId: string): boolean {
    return providerId === "codex";
  }

  async handleServerRequest(
    _ctx: unknown,
    _method: string,
    _params: Record<string, unknown>,
  ): Promise<AcpExtensionHandled | undefined> {
    return undefined;
  }
}
