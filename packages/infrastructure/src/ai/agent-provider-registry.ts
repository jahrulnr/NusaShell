import type { AgentProvider, AgentProviderRegistryPort } from "@nusashell/application";

/** Immutable startup registry; provider credentials remain inside adapters. */
export class AgentProviderRegistry implements AgentProviderRegistryPort {
  private readonly providers = new Map<string, AgentProvider>();

  constructor(providers: readonly AgentProvider[]) {
    for (const provider of providers) {
      if (this.providers.has(provider.id)) {
        throw new Error(`Duplicate agent provider: ${provider.id}`);
      }
      this.providers.set(provider.id, provider);
    }
  }

  get(providerId: string): AgentProvider | undefined {
    return this.providers.get(providerId);
  }
}
