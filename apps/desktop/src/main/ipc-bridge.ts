import { ipcMain, BrowserWindow } from "electron";
import { randomUUID } from "node:crypto";
import {
  MessageRouter,
  mapDomainEvent,
  type ProtocolError,
} from "@nusashell/transport-ws";
import type { BootstrapResult } from "@nusashell/backend";
import type { LoggerPort } from "@nusashell/application";

/**
 * Bridges renderer IPC requests to the application command/query bus using
 * the same MessageRouter that the WebSocket transport uses. This keeps one
 * dispatch path for method → bus mapping and lets the renderer drop the
 * loopback WebSocket entirely.
 *
 * Channel: `shell:request` (renderer → main)
 * Args:    (method: string, payload?: unknown, opts?: { timeoutMs?: number })
 * Returns: { ok: true, result } | { ok: false, error: { code, message, details? } }
 */
export class IpcRequestBridge {
  private readonly router: MessageRouter;

  constructor(
    backend: BootstrapResult,
    private readonly logger?: LoggerPort,
  ) {
    this.router = new MessageRouter({
      commandBus: backend.container.commandBus,
      queryBus: backend.container.queryBus,
      ...(logger ? { logger } : {}),
    });
  }

  register(): void {
    ipcMain.handle("shell:request", async (_event, method: string, payload?: unknown, opts?: { timeoutMs?: number }) => {
      const requestId = randomUUID();
      const timeoutMs = opts?.timeoutMs ?? 30_000;
      const raw = { kind: "request", id: requestId, method, payload: payload ?? {} };

      // Race the router against a timeout. The application bus already has
      // its own timeouts for long-running commands (agent.run: 30m), but IPC
      // needs an upper bound too so a hung handler does not block the
      // renderer forever. Use the caller-provided timeout when given.
      try {
        const result = await raceWithTimeout(
          this.router.handle(raw),
          timeoutMs,
          requestId,
        );
        return result;
      } catch (error) {
        const response = mapIpcFailureToResponse(requestId, error);
        this.logger?.error(
          "IPC shell:request failed method=%s: %s",
          method,
          response.error.message,
        );
        return response;
      }
    });
  }

  close(): void {
    this.router.close();
  }
}

/**
 * Bridges application events to all renderer windows via IPC.
 * Uses the same `mapDomainEvent` mapper as the WebSocket publisher so event
 * payloads are identical in shape — the renderer does not need to know
 * whether the transport was WS or IPC.
 *
 * Channel: `shell:event` (main → renderer)
 * Payload: { event: string, payload: unknown, sequence: number }
 */
export class IpcEventBridge {
  private sequenceCounter = 0;
  private disposed = false;

  constructor(
    private readonly backend: BootstrapResult,
    private readonly logger?: LoggerPort,
  ) {}

  register(): void {
    this.backend.container.eventDispatcher.onAny({
      handle: async (event) => {
        if (this.disposed) return;
        const sequence = ++this.sequenceCounter;
        const envelope = mapDomainEvent(event, sequence);
        if (!envelope) return;
        const frame = { event: envelope.event, payload: envelope.payload, sequence, emittedAt: Date.now() };
        for (const window of BrowserWindow.getAllWindows()) {
          if (window.isDestroyed()) continue;
          try {
            window.webContents.send("shell:event", frame);
          } catch (error) {
            this.logger?.warn("IPC event send failed window=%s: %s", window.id, error instanceof Error ? error.message : String(error));
          }
        }
      },
    });
  }

  dispose(): void {
    this.disposed = true;
  }
}

/** Rejected by {@link raceWithTimeout} when the IPC budget elapses. */
export class IpcRequestTimeoutError extends Error {
  readonly code = "TIMEOUT";

  constructor(readonly timeoutMs: number) {
    super(`IPC request timed out after ${timeoutMs}ms`);
    this.name = "IpcRequestTimeoutError";
  }
}

/**
 * Map a thrown value from the IPC handler into a ResponseEnvelope the
 * renderer can unwrap. Never uses `String(object)` (that produced the UI
 * text "Turn failed: [object Object]" after IPC timeouts).
 */
export function mapIpcFailureToResponse(
  requestId: string,
  error: unknown,
): {
  readonly kind: "response";
  readonly id: string;
  readonly ok: false;
  readonly error: { readonly code: string; readonly message: string; readonly details?: Readonly<Record<string, unknown>> };
} {
  // Already a full envelope from an older caller path.
  if (
    typeof error === "object"
    && error !== null
    && "kind" in error
    && (error as { kind?: unknown }).kind === "response"
    && "ok" in error
    && (error as { ok?: unknown }).ok === false
    && typeof (error as { error?: { code?: unknown; message?: unknown } }).error === "object"
    && (error as { error?: { code?: unknown; message?: unknown } }).error
  ) {
    const nested = (error as {
      error: { code?: unknown; message?: unknown; details?: Readonly<Record<string, unknown>> };
    }).error;
    const code = typeof nested.code === "string" && nested.code ? nested.code : "IPC_ERROR";
    const message = typeof nested.message === "string" && nested.message.trim()
      ? nested.message.trim()
      : code === "TIMEOUT"
        ? "IPC request timed out"
        : "IPC request failed";
    return {
      kind: "response",
      id: requestId,
      ok: false,
      error: {
        code,
        message,
        ...(nested.details ? { details: nested.details } : {}),
      },
    };
  }

  if (error instanceof IpcRequestTimeoutError) {
    return {
      kind: "response",
      id: requestId,
      ok: false,
      error: {
        code: error.code,
        message: error.message,
        details: { timeoutMs: error.timeoutMs },
      },
    };
  }

  if (error instanceof Error) {
    const code =
      "code" in error && typeof (error as { code?: unknown }).code === "string" && (error as { code: string }).code
        ? (error as { code: string }).code
        : "IPC_ERROR";
    const message = error.message.trim() || "IPC request failed";
    return {
      kind: "response",
      id: requestId,
      ok: false,
      error: { code, message },
    };
  }

  if (isProtocolErrorLike(error)) {
    return {
      kind: "response",
      id: requestId,
      ok: false,
      error: {
        code: error.code,
        message: error.message,
        ...(error.details ? { details: error.details } : {}),
      },
    };
  }

  if (typeof error === "string" && error.trim()) {
    return {
      kind: "response",
      id: requestId,
      ok: false,
      error: { code: "IPC_ERROR", message: error.trim() },
    };
  }

  return {
    kind: "response",
    id: requestId,
    ok: false,
    error: { code: "IPC_ERROR", message: "IPC request failed" },
  };
}

/** Race a promise against a timeout, rejecting with {@link IpcRequestTimeoutError}. */
export function raceWithTimeout<T>(
  promise: Promise<T>,
  timeoutMs: number,
  _requestId: string,
): Promise<T> {
  if (timeoutMs <= 0) return promise;
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => {
      reject(new IpcRequestTimeoutError(timeoutMs));
    }, timeoutMs);
    promise.then(
      (value) => { clearTimeout(timer); resolve(value); },
      (error) => { clearTimeout(timer); reject(error); },
    );
  });
}

/** Type guard for ProtocolError-shaped objects. */
export function isProtocolErrorLike(value: unknown): value is ProtocolError {
  return typeof value === "object" && value !== null && "code" in value && "message" in value
    && typeof (value as ProtocolError).code === "string"
    && typeof (value as ProtocolError).message === "string";
}
