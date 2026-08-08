import { randomUUID } from "node:crypto";

export type AsyncToolStatus = "running" | "ok" | "fail" | "killed";
export type AsyncToolKind = "mcp" | "subagent";
export type AsyncToolEndReason = "completed" | "killed" | "failed";

export interface AsyncToolHandle {
  readonly handleId: string;
  readonly conversationId: string;
  readonly kind: AsyncToolKind;
  readonly pluginId?: string;
  readonly toolName: string;
  readonly args: Readonly<Record<string, unknown>>;
  readonly traceId?: string;
  readonly startedAt: Date;
  status: AsyncToolStatus;
  endedAt?: Date;
  result?: unknown;
  error?: string;
  endReason?: AsyncToolEndReason;
  tail: string;
}

export interface AsyncToolSpawnInput {
  readonly conversationId: string;
  readonly kind: AsyncToolKind;
  readonly pluginId?: string;
  readonly toolName: string;
  readonly args: Readonly<Record<string, unknown>>;
  readonly traceId?: string;
  readonly maxRuntimeMs?: number;
  /** Work function. Receives the handle's abort signal and handleId so kill can cancel the in-flight call and progress can be appended. */
  readonly work: (signal: AbortSignal, handleId: string) => Promise<unknown>;
}

export interface AsyncToolPeekResult {
  readonly handleId: string;
  readonly kind: AsyncToolKind;
  readonly pluginId?: string;
  readonly toolName: string;
  readonly status: AsyncToolStatus;
  readonly tail: string;
  readonly result?: unknown;
  readonly error?: string;
  readonly endReason?: AsyncToolEndReason;
  readonly startedAt: string;
  readonly endedAt?: string;
  /** True when a wait was cut short by an abort signal while still running. */
  readonly interrupted?: boolean;
}

export interface AsyncToolWaitResult extends AsyncToolPeekResult {}

export interface AsyncToolRuntimeOptions {
  readonly tailMaxBytes?: number;
  readonly onStarted?: ((handle: AsyncToolHandle) => void) | undefined;
  readonly onEnded?: ((handle: AsyncToolHandle) => void) | undefined;
  /** Called when a progress update is appended to the tail buffer. */
  readonly onProgress?: ((handle: AsyncToolHandle, tail: string, bytes: number) => void) | undefined;
}

const DEFAULT_TAIL_MAX_BYTES = 256 * 1024;

/**
 * Broker-owned registry of async (background) tool handles.
 *
 * Handles outlive the spawning turn. The agent starts a background call via
 * `spawn`, then polls with `peek` (non-blocking) or `wait` (bounded blocking).
 * `kill` soft-cancels a handle. `killAllForConversation` is used for
 * conversation delete / app quit cleanup.
 *
 * SoT is in-memory only (same runtime lock as PluginRuntimeManager).
 */
export class AsyncToolRuntime {
  private readonly handles = new Map<string, AsyncToolHandle>();
  private readonly waiters = new Map<string, Array<() => void>>();
  private readonly abortControllers = new Map<string, AbortController>();
  private readonly tailMaxBytes: number;
  private readonly onStarted?: ((handle: AsyncToolHandle) => void) | undefined;
  private readonly onEnded?: ((handle: AsyncToolHandle) => void) | undefined;
  private readonly onProgress?: ((handle: AsyncToolHandle, tail: string, bytes: number) => void) | undefined;

  constructor(options: AsyncToolRuntimeOptions = {}) {
    this.tailMaxBytes = options.tailMaxBytes ?? DEFAULT_TAIL_MAX_BYTES;
    this.onStarted = options.onStarted;
    this.onEnded = options.onEnded;
    this.onProgress = options.onProgress;
  }

  async spawn(input: AsyncToolSpawnInput): Promise<AsyncToolHandle> {
    const handleId = randomUUID();
    const handle: AsyncToolHandle = {
      handleId,
      conversationId: input.conversationId,
      kind: input.kind,
      ...(input.pluginId ? { pluginId: input.pluginId } : {}),
      toolName: input.toolName,
      args: input.args,
      ...(input.traceId ? { traceId: input.traceId } : {}),
      startedAt: new Date(),
      status: "running",
      tail: "",
    };
    this.handles.set(handleId, handle);
    const abortController = new AbortController();
    this.abortControllers.set(handleId, abortController);
    this.onStarted?.(handle);

    // Fire-and-forget the work; settle the handle when it resolves/rejects.
    let timer: ReturnType<typeof setTimeout> | undefined;
    if (input.maxRuntimeMs && input.maxRuntimeMs > 0) {
      timer = setTimeout(() => {
        if (this.handles.get(handleId)?.status === "running") {
          abortController.abort();
          this.settle(handleId, "fail", undefined, `Max runtime exceeded (${input.maxRuntimeMs}ms)`, "failed");
        }
      }, input.maxRuntimeMs);
    }

    input
      .work(abortController.signal, handleId)
      .then((result) => {
        if (timer) clearTimeout(timer);
        const current = this.handles.get(handleId);
        if (current && current.status === "running") {
          this.settle(handleId, "ok", result, undefined, "completed");
        }
      })
      .catch((error) => {
        if (timer) clearTimeout(timer);
        const current = this.handles.get(handleId);
        if (current && current.status === "running") {
          const message = error instanceof Error ? error.message : String(error);
          // If the abort signal fired, this is a kill, not a failure.
          if (abortController.signal.aborted) {
            this.settle(handleId, "killed", undefined, "Killed by request", "killed");
          } else {
            this.settle(handleId, "fail", undefined, message, "failed");
          }
        }
      })
      .finally(() => {
        this.abortControllers.delete(handleId);
      });

    return handle;
  }

  peek(handleId: string): AsyncToolPeekResult | undefined {
    const h = this.handles.get(handleId);
    if (!h) return undefined;
    return this.toPeekResult(h);
  }

  async wait(handleId: string, timeoutMs: number): Promise<AsyncToolWaitResult | undefined> {
    const h = this.handles.get(handleId);
    if (!h) return undefined;
    if (h.status !== "running") return this.toPeekResult(h);

    return new Promise<AsyncToolWaitResult | undefined>((resolve) => {
      let settled = false;
      const finish = () => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        // Remove this waiter from the list.
        const list = this.waiters.get(handleId);
        if (list) {
          const idx = list.indexOf(wake);
          if (idx >= 0) list.splice(idx, 1);
          if (list.length === 0) this.waiters.delete(handleId);
        }
        const current = this.handles.get(handleId);
        resolve(current ? this.toPeekResult(current) : undefined);
      };
      const wake = () => finish();
      const timer = setTimeout(() => finish(), Math.max(0, timeoutMs));
      // Register the waiter so settle() can wake us.
      const list = this.waiters.get(handleId) ?? [];
      list.push(wake);
      this.waiters.set(handleId, list);
    });
  }

  kill(handleId: string): AsyncToolPeekResult | undefined {
    const h = this.handles.get(handleId);
    if (!h) return undefined;
    if (h.status === "running") {
      // Abort the in-flight MCP call so the plugin can stop the work.
      this.abortControllers.get(handleId)?.abort();
      this.settle(handleId, "killed", undefined, "Killed by request", "killed");
    }
    return this.toPeekResult(h);
  }

  killAllForConversation(conversationId: string): number {
    let count = 0;
    for (const h of this.handles.values()) {
      if (h.conversationId === conversationId && h.status === "running") {
        this.abortControllers.get(h.handleId)?.abort();
        this.settle(h.handleId, "killed", undefined, "Killed by conversation cleanup", "killed");
        count++;
      }
    }
    return count;
  }

  list(conversationId: string): readonly AsyncToolPeekResult[] {
    const result: AsyncToolPeekResult[] = [];
    for (const h of this.handles.values()) {
      if (h.conversationId === conversationId) {
        result.push(this.toPeekResult(h));
      }
    }
    return result;
  }

  appendTail(handleId: string, text: string): void {
    const h = this.handles.get(handleId);
    if (!h) return;
    if (h.status !== "running") return;
    h.tail = (h.tail + text).slice(-this.tailMaxBytes);
    this.onProgress?.(h, h.tail, h.tail.length);
  }

  dispose(): void {
    for (const h of this.handles.values()) {
      if (h.status === "running") {
        this.abortControllers.get(h.handleId)?.abort();
        this.settle(h.handleId, "killed", undefined, "Runtime disposed", "killed");
      }
    }
    this.handles.clear();
    this.abortControllers.clear();
  }

  private settle(
    handleId: string,
    status: AsyncToolStatus,
    result: unknown,
    error: string | undefined,
    endReason: AsyncToolEndReason,
  ): void {
    const h = this.handles.get(handleId);
    if (!h) return;
    h.status = status;
    h.endedAt = new Date();
    if (result !== undefined) h.result = result;
    if (error !== undefined) h.error = error;
    h.endReason = endReason;
    this.onEnded?.(h);
    // Wake any waiters.
    const waiters = this.waiters.get(handleId);
    if (waiters) {
      this.waiters.delete(handleId);
      for (const wake of waiters) wake();
    }
  }

  private toPeekResult(h: AsyncToolHandle): AsyncToolPeekResult {
    return {
      handleId: h.handleId,
      kind: h.kind,
      ...(h.pluginId ? { pluginId: h.pluginId } : {}),
      toolName: h.toolName,
      status: h.status,
      tail: h.tail,
      ...(h.result !== undefined ? { result: h.result } : {}),
      ...(h.error !== undefined ? { error: h.error } : {}),
      ...(h.endReason !== undefined ? { endReason: h.endReason } : {}),
      startedAt: h.startedAt.toISOString(),
      ...(h.endedAt ? { endedAt: h.endedAt.toISOString() } : {}),
    };
  }
}
