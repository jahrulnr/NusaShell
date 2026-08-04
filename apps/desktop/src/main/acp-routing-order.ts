/**
 * Pure try-order computation for ACP provider routing.
 *
 * Extracted from `AcpProviderStore` so the algorithm is testable without
 * filesystem mocking. Both `getRouting()` and `resolveTryOrder()` delegate here.
 */

export interface ComputeAcpTryOrderInput {
  readonly defaultProviderId?: string;
  readonly fallbackProviderIds?: readonly string[];
  /** Connected provider IDs in manifest order (enabled + authStatus=connected). */
  readonly connectedIds: readonly string[];
}

/**
 * Compute the effective try-order:
 * 1. the configured default, if it is connected;
 * 2. configured fallback ids, filtered to connected;
 * 3. every remaining connected provider in manifest order.
 */
export function computeAcpTryOrder(input: ComputeAcpTryOrderInput): string[] {
  const connectedSet = new Set(input.connectedIds);
  const order: string[] = [];
  const seen = new Set<string>();

  if (input.defaultProviderId && connectedSet.has(input.defaultProviderId)) {
    order.push(input.defaultProviderId);
    seen.add(input.defaultProviderId);
  }

  for (const id of input.fallbackProviderIds ?? []) {
    if (connectedSet.has(id) && !seen.has(id)) {
      order.push(id);
      seen.add(id);
    }
  }

  for (const id of input.connectedIds) {
    if (!seen.has(id)) {
      order.push(id);
      seen.add(id);
    }
  }

  return order;
}
