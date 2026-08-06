import type {
  AgentProvider,
  AgentProviderRequest,
  AgentProviderResult,
} from "../agent/ports/agent-provider.port.js";
import { toTelemetryUsage } from "./telemetry-usage.js";
import type { TelemetryPort } from "./telemetry.port.js";
import type { ProviderRequestTelemetry } from "./telemetry.types.js";

/** Minimal monotonic-ish clock seam so timing can be faked in tests. */
export interface MillisClock {
  now(): number;
}

const SYSTEM_CLOCK: MillisClock = { now: () => Date.now() };

/**
 * Transparent {@link AgentProvider} decorator that records one
 * {@link ProviderRequestTelemetry} per `complete()` call. Because it wraps each
 * concrete provider, router failover naturally produces one record per
 * candidate tried, and the compaction summarizer's `round: 0` sample is
 * captured too. Recording is best-effort and never alters the provider result
 * or throws on its own.
 */
export class TelemetryAgentProvider implements AgentProvider {
  readonly id: string;
  // Only present when the inner provider declares it, matching the optional
  // interface property under `exactOptionalPropertyTypes`.
  readonly managesAttemptBudget?: boolean;

  constructor(
    private readonly inner: AgentProvider,
    private readonly telemetry: TelemetryPort,
    private readonly clock: MillisClock = SYSTEM_CLOCK,
  ) {
    this.id = inner.id;
    if (inner.managesAttemptBudget !== undefined) {
      this.managesAttemptBudget = inner.managesAttemptBudget;
    }
  }

  async complete(request: AgentProviderRequest): Promise<AgentProviderResult> {
    const startedAtMs = this.clock.now();
    try {
      const result = await this.inner.complete(request);
      this.record(request, startedAtMs, {
        status: "completed",
        ...(result.status ? { finishReason: result.status } : {}),
      }, result);
      return result;
    } catch (error) {
      this.record(request, startedAtMs, {
        status: "failed",
        ...(errorCodeOf(error) ? { errorCode: errorCodeOf(error)! } : {}),
      });
      throw error;
    }
  }

  private record(
    request: AgentProviderRequest,
    startedAtMs: number,
    outcome: ProviderRequestTelemetry["outcome"],
    result?: AgentProviderResult,
  ): void {
    try {
      const completedAtMs = this.clock.now();
      const providerId = result?.providerId ?? this.inner.id;
      const model = result?.model ?? request.model;
      const record: ProviderRequestTelemetry = {
        kind: "provider_request",
        schemaVersion: 1,
        traceId: request.traceId,
        timestamp: new Date(completedAtMs).toISOString(),
        round: request.round,
        ...(providerId ? { providerId } : {}),
        ...(model ? { model } : {}),
        ...(result?.usage ? { usage: toTelemetryUsage(result.usage) } : {}),
        timing: {
          startedAt: new Date(startedAtMs).toISOString(),
          completedAt: new Date(completedAtMs).toISOString(),
          latencyMs: Math.max(0, completedAtMs - startedAtMs),
        },
        outcome,
      };
      this.telemetry.recordProviderRequest(record);
    } catch {
      // Telemetry must never break a turn.
    }
  }
}

/** Best-effort extraction of a stable error code / name for telemetry. */
function errorCodeOf(error: unknown): string | undefined {
  if (error && typeof error === "object") {
    const code = (error as { code?: unknown }).code;
    if (typeof code === "string" && code.length > 0) return code;
    const name = (error as { name?: unknown }).name;
    if (typeof name === "string" && name.length > 0) return name;
  }
  return undefined;
}

/**
 * Wrap a provider so its requests are recorded. Returns the original provider
 * untouched when the sink is absent, keeping the no-telemetry path allocation
 * free.
 */
export function withTelemetry(
  provider: AgentProvider,
  telemetry: TelemetryPort | undefined,
  clock: MillisClock = SYSTEM_CLOCK,
): AgentProvider {
  if (!telemetry) return provider;
  return new TelemetryAgentProvider(provider, telemetry, clock);
}
