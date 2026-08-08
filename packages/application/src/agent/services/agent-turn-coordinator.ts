import { ConflictError } from "../../errors/conflict-error.js";

/** Owns cancellation signals for active in-process agent turns. */
export class AgentTurnCoordinator {
  private readonly active = new Map<string, AbortController>();

  get activeCount(): number {
    return this.active.size;
  }

  async run<T>(
    traceId: string,
    execute: (signal: AbortSignal) => Promise<T>,
  ): Promise<T> {
    if (this.active.has(traceId)) {
      throw new ConflictError("An agent turn with this trace ID is already active", { traceId });
    }
    const controller = new AbortController();
    this.active.set(traceId, controller);
    try {
      return await execute(controller.signal);
    } finally {
      this.active.delete(traceId);
    }
  }

  cancel(traceId: string): boolean {
    const controller = this.active.get(traceId);
    if (!controller) return false;
    controller.abort(new Error("Agent turn interrupted by the user"));
    return true;
  }

  /** Request cancellation for every in-process turn during graceful shutdown. */
  cancelAll(): number {
    let cancelled = 0;
    for (const controller of this.active.values()) {
      controller.abort(new Error("Agent turn interrupted by application shutdown"));
      cancelled += 1;
    }
    return cancelled;
  }

  /** Wait until cancelled turns have run their finally/seal cleanup. */
  async waitForIdle(timeoutMs = 5_000): Promise<void> {
    const deadline = Date.now() + timeoutMs;
    while (this.active.size > 0 && Date.now() < deadline) {
      await new Promise<void>((resolve) => setTimeout(resolve, 10));
    }
  }
}
