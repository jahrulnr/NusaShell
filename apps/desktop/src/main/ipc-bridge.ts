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
        this.logger?.error("IPC shell:request failed method=%s: %s", method, error instanceof Error ? error.message : String(error));
        return {
          kind: "response" as const,
          id: requestId,
          ok: false,
          error: {
            code: "IPC_ERROR",
            message: error instanceof Error ? error.message : String(error),
          },
        };
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

/** Race a promise against a timeout, producing a ProtocolError-shaped reject. */
function raceWithTimeout<T>(
  promise: Promise<T>,
  timeoutMs: number,
  requestId: string,
): Promise<T> {
  if (timeoutMs <= 0) return promise;
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => {
      reject({
        kind: "response",
        id: requestId,
        ok: false,
        error: {
          code: "TIMEOUT",
          message: `IPC request timed out after ${timeoutMs}ms`,
        },
      } as unknown as Error);
    }, timeoutMs);
    promise.then(
      (value) => { clearTimeout(timer); resolve(value); },
      (error) => { clearTimeout(timer); reject(error); },
    );
  });
}

/** Re-throw a ProtocolError-shaped object as a real Error for the catch block. */
export function isProtocolErrorLike(value: unknown): value is ProtocolError {
  return typeof value === "object" && value !== null && "code" in value && "message" in value;
}
