import type { AgentTurnTelemetry, ProviderRequestTelemetry } from "./telemetry.types.js";

/**
 * Sink for token-efficiency telemetry. Implementations MUST be
 * fire-and-forget: they never throw and never add latency to the agent turn
 * (buffer / append asynchronously). A failing telemetry sink must not fail a
 * user turn.
 */
export interface TelemetryPort {
  recordProviderRequest(record: ProviderRequestTelemetry): void;
  recordTurn(record: AgentTurnTelemetry): void;
}
