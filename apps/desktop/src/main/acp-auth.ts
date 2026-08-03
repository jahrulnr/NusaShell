import type { ProbeAcpProviderCommand, ProbeAcpProviderResult } from "@nusashell/application";
import type { AcpProviderPublic, AcpProviderSaveInput } from "../shared/acp-provider-contract.js";
import type { AcpProviderStore } from "./acp-provider-store.js";

export interface AcpProbeOptions {
  /**
   * When true (Connect button), fall back to the configured/manifest auth
   * method after a file-auth probe fails — this may open a browser OAuth flow.
   * Silent refresh must leave this false so restarts never force OAuth.
   */
  readonly interactive?: boolean;
}

type CommandBus = {
  execute(command: ProbeAcpProviderCommand): Promise<unknown>;
};

/**
 * Probe an ACP provider preferring existing CLI file auth.
 *
 * Phase 1 always tries without `authMethodId` (Cursor `~/.config/cursor`,
 * Codex `~/.codex`). Phase 2 (interactive only) retries with the configured
 * auth method when phase 1 fails — that is the only path that should open
 * a browser for OAuth.
 *
 * Silent refresh only upgrades to `connected`; it never downgrades a prior
 * connected flag on a transient spawn failure.
 */
export async function probeAcpProviderAuth(
  store: AcpProviderStore,
  commandBus: CommandBus,
  providerId: string,
  options: AcpProbeOptions = {},
): Promise<AcpProviderPublic | null> {
  const provider = await store.getEffective(providerId);
  if (!provider) throw new Error(`ACP provider not found: ${providerId}`);

  const interactive = options.interactive === true;
  // Prefer an explicit Configure selection; otherwise the first advertised
  // interactive method (e.g. cursor_login) is only used for Connect retries.
  const interactiveAuthMethodId =
    provider.config.authMethodId
    ?? provider.manifest.authMethodId
    ?? provider.manifest.authMethodIds?.[0];
  const base = {
    providerId: provider.manifest.id,
    command: provider.config.command || provider.manifest.command,
    args: provider.config.args ?? provider.manifest.args,
    ...(provider.manifest.env ? { env: provider.manifest.env } : {}),
  };

  // Phase 1: file / env auth only — never force interactive OAuth.
  const fileAuthResult = await runProbe(commandBus, base);
  if (fileAuthResult.ok) {
    await store.save({ providerId, authStatus: "connected", authCheckedAt: new Date().toISOString(), authError: "" });
    return store.getEffective(providerId);
  }

  // Phase 2: interactive Connect may retry with an explicit auth method.
  if (interactive && interactiveAuthMethodId) {
    const interactiveResult = await runProbe(commandBus, { ...base, authMethodId: interactiveAuthMethodId });
    const authCheckedAt = new Date().toISOString();
    if (interactiveResult.ok) {
      await store.save({ providerId, authStatus: "connected", authCheckedAt, authError: "" });
    } else {
      const errorSave: AcpProviderSaveInput = {
        providerId,
        authStatus: "needs-auth",
        authCheckedAt,
        ...(interactiveResult.error ? { authError: interactiveResult.error } : {}),
      };
      await store.save(errorSave);
    }
    return store.getEffective(providerId);
  }

  if (interactive) {
    const authCheckedAt = new Date().toISOString();
    const errorSave: AcpProviderSaveInput = {
      providerId,
      authStatus: "needs-auth",
      authCheckedAt,
      ...(fileAuthResult.error ? { authError: fileAuthResult.error } : {}),
    };
    await store.save(errorSave);
  }
  // Silent path: leave prior authStatus untouched on failure.
  return store.getEffective(providerId);
}

/**
 * After backend boot, try file-auth probes for enabled providers that are not
 * already marked connected. Restores the Connected badge across dev/prod
 * userData switches without opening a browser.
 */
export async function refreshAcpAuthStatuses(
  store: AcpProviderStore,
  commandBus: CommandBus,
  log?: (message: string) => void,
): Promise<void> {
  const providers = await store.list();
  for (const provider of providers) {
    if (!provider.config.enabled) continue;
    if (provider.status === "not-configured" || provider.status === "disabled") continue;
    if (provider.config.authStatus === "connected") continue;
    try {
      const updated = await probeAcpProviderAuth(store, commandBus, provider.manifest.id, { interactive: false });
      if (updated?.config.authStatus === "connected") {
        log?.(`ACP silent reconnect: ${provider.manifest.id}`);
      }
    } catch (error) {
      log?.(`ACP silent reconnect skipped for ${provider.manifest.id}: ${error instanceof Error ? error.message : String(error)}`);
    }
  }
}

async function runProbe(
  commandBus: CommandBus,
  provider: ProbeAcpProviderCommand["provider"],
): Promise<ProbeAcpProviderResult> {
  const command: ProbeAcpProviderCommand = {
    kind: "probe-acp-provider",
    provider,
  };
  return (await commandBus.execute(command)) as ProbeAcpProviderResult;
}
