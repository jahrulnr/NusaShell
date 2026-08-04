// Event skew checker — measures IPC event delivery latency.
// When the main process emits a shell:event frame, it stamps `emittedAt`
// (ms epoch). The receiver checks skew on receive to detect event bus
// stalls or main-process blocking that delays delivery.
//
// Pure module: no DOM, no Electron. Safe to import from preload, renderer,
// and tests.

export const SKEW_THRESHOLD_MS = 250;
export const FLOOD_WINDOW_MS = 5000;

export interface EventSkewFrame {
  readonly event: string;
  readonly sequence?: number;
  readonly emittedAt?: number;
}

export interface EventSkewContext {
  readonly now: number;
  readonly thresholdMs?: number;
  readonly warn: (message: string) => void;
  readonly lastWarnAt: number;
}

export interface EventSkewResult {
  readonly warned: boolean;
  readonly lastWarnAt: number;
}

/**
 * Check whether an incoming event frame has excessive delivery skew.
 * Logs a warning once per FLOOD_WINDOW_MS to avoid spam.
 */
export function checkEventSkew(frame: EventSkewFrame, ctx: EventSkewContext): EventSkewResult {
  if (frame.emittedAt === undefined || frame.emittedAt === null) {
    return { warned: false, lastWarnAt: ctx.lastWarnAt };
  }
  const threshold = ctx.thresholdMs ?? SKEW_THRESHOLD_MS;
  const skewMs = ctx.now - frame.emittedAt;
  if (skewMs <= threshold) {
    return { warned: false, lastWarnAt: ctx.lastWarnAt };
  }
  if (ctx.lastWarnAt > 0 && ctx.now - ctx.lastWarnAt < FLOOD_WINDOW_MS) {
    return { warned: false, lastWarnAt: ctx.lastWarnAt };
  }
  ctx.warn(`ipc.skew event=${frame.event} skewMs=${skewMs} sequence=${frame.sequence ?? "?"}`);
  return { warned: true, lastWarnAt: ctx.now };
}
