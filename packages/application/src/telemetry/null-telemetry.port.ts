import type { TelemetryPort } from "./telemetry.port.js";

/** No-op telemetry sink used when telemetry is disabled or not configured. */
export class NullTelemetryPort implements TelemetryPort {
  recordProviderRequest(): void {
    /* intentionally empty */
  }

  recordTurn(): void {
    /* intentionally empty */
  }
}
