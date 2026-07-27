import { RequestTimeoutError } from "../errors/request-timeout.error.js";
import { ConnectionClosedError } from "../errors/connection-closed.error.js";

interface PendingRequest {
  readonly resolve: (value: unknown) => void;
  readonly reject: (error: unknown) => void;
  readonly timer: ReturnType<typeof setTimeout>;
}

export class RequestManager {
  private readonly pending = new Map<string, PendingRequest>();
  private defaultTimeoutMs: number;

  constructor(defaultTimeoutMs = 30000) {
    this.defaultTimeoutMs = defaultTimeoutMs;
  }

  register(
    requestId: string,
    timeoutMs?: number,
  ): Promise<unknown> {
    const timeout = timeoutMs ?? this.defaultTimeoutMs;

    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(requestId);
        reject(new RequestTimeoutError(requestId, timeout));
      }, timeout);

      this.pending.set(requestId, {
        resolve: resolve as (value: unknown) => void,
        reject,
        timer,
      });
    });
  }

  resolve(requestId: string, result: unknown): boolean {
    const entry = this.pending.get(requestId);
    if (!entry) return false;

    clearTimeout(entry.timer);
    this.pending.delete(requestId);
    entry.resolve(result);
    return true;
  }

  reject(requestId: string, error: unknown): boolean {
    const entry = this.pending.get(requestId);
    if (!entry) return false;

    clearTimeout(entry.timer);
    this.pending.delete(requestId);
    entry.reject(error);
    return true;
  }

  rejectAll(error: unknown): void {
    for (const [, entry] of this.pending) {
      clearTimeout(entry.timer);
      entry.reject(error);
    }
    this.pending.clear();
  }

  has(requestId: string): boolean {
    return this.pending.has(requestId);
  }

  get size(): number {
    return this.pending.size;
  }

  close(): void {
    this.rejectAll(new ConnectionClosedError());
  }

  setDefaultTimeout(timeoutMs: number): void {
    this.defaultTimeoutMs = timeoutMs;
  }
}
