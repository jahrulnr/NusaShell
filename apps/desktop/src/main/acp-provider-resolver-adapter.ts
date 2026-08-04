import type { AcpProviderResolverPort, AcpResolverCandidate, AcpResolverResult, AcpProviderDescriptor } from "@nusashell/application";
import type { AcpProviderStore } from "./acp-provider-store.js";
import type { AcpProviderPublic } from "../shared/acp-provider-contract.js";

/**
 * Adapts the desktop-side AcpProviderStore to the application-layer
 * AcpProviderResolverPort. The store knows about connected providers and
 * routing preferences; this adapter translates that into the resolver
 * contract used by the subagent gateway.
 */
export class AcpProviderResolverAdapter implements AcpProviderResolverPort {
  constructor(private readonly store: AcpProviderStore) {}

  async resolve(): Promise<AcpResolverResult> {
    const tryOrder = await this.store.resolveTryOrder();
    const candidates = new Map<string, AcpResolverCandidate>();
    for (const providerId of tryOrder) {
      const provider = await this.store.getEffective(providerId);
      if (!provider) continue;
      const descriptor = this.toDescriptor(provider);
      candidates.set(providerId, {
        providerId,
        descriptor,
        ...(provider.config.preferredConfig ? { preferredConfig: provider.config.preferredConfig } : {}),
      });
    }
    return { tryOrder, candidates };
  }

  private toDescriptor(provider: AcpProviderPublic): AcpProviderDescriptor {
    const manifest = provider.manifest;
    const config = provider.config;
    const command = config.command || manifest.command;
    const args = config.args ?? manifest.args;
    const authMethodId = config.authMethodId ?? manifest.authMethodId;
    return {
      providerId: manifest.id,
      command,
      args,
      ...(authMethodId ? { authMethodId } : {}),
      ...(manifest.env ? { env: manifest.env } : {}),
    };
  }
}
