import type {
  AgentProvider,
  AgentProviderRequest,
  AgentProviderResult,
} from "../ports/agent-provider.port.js";
import type { LoggerPort } from "../../plugin/ports/logger.port.js";

export type AgentProviderStrategy = "failover" | "round-robin" | "switch";

export interface RoutedAgentProviderOptions {
  readonly providers: readonly AgentProvider[];
  readonly preferredProviderId: string;
  readonly strategy: AgentProviderStrategy;
  readonly totalAttemptBudget: number;
  readonly logger?: LoggerPort;
  /**
   * Shared round-robin cursor across turns. When omitted, a per-instance
   * cursor is used (each turn starts from 0). Pass a process-lifetime cursor
   * from the composition root for true cross-turn rotation.
   */
  readonly roundRobinCursor?: { value: number };
}

/**
 * Per-turn provider router. A successful provider is pinned for later tool
 * rounds so one turn cannot oscillate between incompatible upstream models.
 */
export class RoutedAgentProvider implements AgentProvider {
  readonly id: string;
  private pinned: AgentProvider | undefined;
  private readonly candidates: readonly AgentProvider[];
  private readonly roundRobinCursor: { value: number };

  constructor(private readonly options: RoutedAgentProviderOptions) {
    this.id = options.preferredProviderId;
    this.roundRobinCursor = options.roundRobinCursor ?? { value: 0 };
    this.candidates = orderCandidates(options, this.roundRobinCursor);
  }

  async complete(request: AgentProviderRequest): Promise<AgentProviderResult> {
    const budget = Math.max(1, Math.min(32, this.options.totalAttemptBudget));
    let attempts = 0;
    const consumeAttempt = () => {
      if (attempts >= budget) return false;
      attempts += 1;
      return true;
    };
    const candidates = this.pinned
      ? [this.pinned, ...this.candidates.filter((provider) => provider.id !== this.pinned?.id)]
      : this.candidates;
    let lastError: unknown;
    for (const provider of candidates) {
      if (attempts >= budget) break;
      try {
        const result = await this.completeWith(
          provider,
          { ...request, consumeAttempt },
          provider.id === this.options.preferredProviderId,
        );
        if (this.pinned?.id !== provider.id) {
          this.options.logger?.info("Agent provider pinned provider=%s previous=%s", provider.id, this.pinned?.id ?? "none");
        }
        this.pinned = provider;
        return result;
      } catch (error) {
        lastError = error;
        if (!isTransientProviderError(error)) throw error;
        this.options.logger?.warn(
          "Agent provider transient failure, failing over provider=%s attempts=%d/%d error=%s",
          provider.id,
          attempts,
          budget,
          error instanceof Error ? error.message : String(error),
        );
      }
    }
    this.options.logger?.error("Agent providers exhausted attempts=%d/%d", attempts, budget);
    throw lastError ?? new Error("AI providers are exhausted");
  }

  private async completeWith(
    provider: AgentProvider,
    request: AgentProviderRequest,
    keepOverride: boolean,
  ): Promise<AgentProviderResult> {
    const budgetedRequest = provider.managesAttemptBudget
      ? request
      : consumeProviderCall(request);
    const routedRequest: AgentProviderRequest = keepOverride
      ? budgetedRequest
      : omitSelectedModel(budgetedRequest);
    const result = await provider.complete(routedRequest);
    return { ...result, providerId: result.providerId ?? provider.id };
  }
}

function consumeProviderCall(request: AgentProviderRequest): AgentProviderRequest {
  if (request.consumeAttempt && !request.consumeAttempt()) {
    throw Object.assign(new Error("Agent provider attempt budget exhausted"), { transient: true });
  }
  const { consumeAttempt: _consumeAttempt, ...withoutBudget } = request;
  return withoutBudget;
}

function omitSelectedModel(request: AgentProviderRequest): AgentProviderRequest {
  const {
    model: _model,
    modelCapabilities: _modelCapabilities,
    ...portableRequest
  } = request;
  return portableRequest;
}

function orderCandidates(options: RoutedAgentProviderOptions, cursor: { value: number }): readonly AgentProvider[] {
  const preferred = options.providers.find((provider) => provider.id === options.preferredProviderId);
  const rest = options.providers.filter((provider) => provider.id !== options.preferredProviderId);
  const ordered = preferred ? [preferred, ...rest] : [...options.providers];
  if (options.strategy === "switch") return ordered.slice(0, 1);
  if (options.strategy !== "round-robin" || ordered.length < 2) return ordered;
  const start = cursor.value % ordered.length;
  cursor.value = (cursor.value + 1) % ordered.length;
  return [...ordered.slice(start), ...ordered.slice(0, start)];
}

function isTransientProviderError(error: unknown): boolean {
  return typeof error === "object"
    && error !== null
    && "transient" in error
    && error.transient === true;
}
