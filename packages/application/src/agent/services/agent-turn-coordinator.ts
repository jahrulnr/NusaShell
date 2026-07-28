import { ConflictError } from "../../errors/conflict-error.js";

/** Owns cancellation signals for active in-process agent turns. */
export class AgentTurnCoordinator {
  private readonly active = new Map<string, AbortController>();

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
}
