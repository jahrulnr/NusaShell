import type { AcpProviderDescriptor } from "./acp-client.port.js";

/** A connected ACP provider candidate for subagent delegation. */
export interface AcpResolverCandidate {
  readonly providerId: string;
  readonly descriptor: AcpProviderDescriptor;
  /** Per-provider config values to apply on spawn (mode, model, etc). */
  readonly preferredConfig?: Readonly<Record<string, string | boolean>>;
}

export interface AcpResolverResult {
  /** Deduplicated try-order of provider IDs (connected only). */
  readonly tryOrder: readonly string[];
  /** Map of providerId → candidate descriptor + preferredConfig. */
  readonly candidates: ReadonlyMap<string, AcpResolverCandidate>;
}

/**
 * Port for resolving ACP provider candidates for subagent delegation.
 * Implemented by the desktop-side AcpProviderStore adapter.
 */
export interface AcpProviderResolverPort {
  /** Resolve the effective try-order and candidate descriptors. */
  resolve(providerIdOverride?: string): Promise<AcpResolverResult>;
}
