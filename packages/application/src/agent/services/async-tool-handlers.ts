import { ApplicationError } from "../../errors/application-error.js";
import type {
  AsyncToolRuntime,
  AsyncToolPeekResult,
  AsyncToolKind,
} from "./async-tool-runtime.js";

const MIN_WAIT_MS = 1_000;
const MAX_WAIT_MS = 300_000;

export interface AsyncRunContext {
  readonly conversationId: string;
  readonly traceId?: string;
  readonly kind: AsyncToolKind;
  readonly pluginId?: string;
  /** Spawn work. Receives the handle's abort signal and handleId so kill can cancel the in-flight call and progress can be appended. */
  readonly spawnWork: (signal: AbortSignal, handleId: string) => Promise<unknown>;
}

export async function execAsyncRun(
  runtime: AsyncToolRuntime,
  args: Readonly<Record<string, unknown>>,
  context: AsyncRunContext,
): Promise<{ handleId: string; status: string; toolName: string }> {
  const toolName = typeof args.tool === "string" ? args.tool.trim() : "";
  if (!toolName) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "async_run requires a non-empty 'tool' name");
  }
  if (!context.conversationId) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "async_run requires a conversation context");
  }
  const toolArgs = (args.args && typeof args.args === "object" ? args.args : {}) as Readonly<Record<string, unknown>>;
  const maxRuntimeMs = typeof args.maxRuntimeMs === "number" && args.maxRuntimeMs > 0 ? args.maxRuntimeMs : undefined;

  const handle = await runtime.spawn({
    conversationId: context.conversationId,
    kind: context.kind,
    ...(context.pluginId ? { pluginId: context.pluginId } : {}),
    toolName,
    args: toolArgs,
    ...(context.traceId ? { traceId: context.traceId } : {}),
    ...(maxRuntimeMs ? { maxRuntimeMs } : {}),
    work: context.spawnWork,
  });
  return { handleId: handle.handleId, status: "running", toolName };
}

export async function execAsyncPeek(
  runtime: AsyncToolRuntime,
  args: Readonly<Record<string, unknown>>,
): Promise<AsyncToolPeekResult> {
  const handleId = typeof args.handleId === "string" ? args.handleId.trim() : "";
  if (!handleId) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "async_peek requires a 'handleId'");
  }
  const peek = runtime.peek(handleId);
  if (!peek) {
    throw new ApplicationError("AGENT_INVALID_INPUT", `Unknown handleId: ${handleId}`);
  }
  return peek;
}

export async function execAsyncWait(
  runtime: AsyncToolRuntime,
  args: Readonly<Record<string, unknown>>,
  signal?: AbortSignal,
): Promise<AsyncToolPeekResult> {
  const handleId = typeof args.handleId === "string" ? args.handleId.trim() : "";
  if (!handleId) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "async_wait requires a 'handleId'");
  }
  const timeoutMs = typeof args.timeoutMs === "number" ? args.timeoutMs : 0;
  if (timeoutMs < MIN_WAIT_MS || timeoutMs > MAX_WAIT_MS) {
    throw new ApplicationError(
      "AGENT_INVALID_INPUT",
      `async_wait timeoutMs must be between ${MIN_WAIT_MS}ms and ${MAX_WAIT_MS}ms`,
    );
  }
  // Race the wait against the abort signal so user steer / turn cancel
  // interrupts the wait early instead of blocking until timeout.
  if (signal) {
    const result = await Promise.race([
      runtime.wait(handleId, timeoutMs),
      new Promise<AsyncToolPeekResult | undefined>((resolve) => {
        if (signal.aborted) {
          resolve(undefined);
          return;
        }
        signal.addEventListener("abort", () => resolve(undefined), { once: true });
      }),
    ]);
    if (!result) {
      // Aborted before settle — return current status (likely "running").
      const peek = runtime.peek(handleId);
      if (peek) {
        return { ...peek, ...(peek.status === "running" ? { interrupted: true } : {}) };
      }
      throw new ApplicationError("AGENT_INVALID_INPUT", `Unknown handleId: ${handleId}`);
    }
    return result;
  }
  const result = await runtime.wait(handleId, timeoutMs);
  if (!result) {
    throw new ApplicationError("AGENT_INVALID_INPUT", `Unknown handleId: ${handleId}`);
  }
  return result;
}

export async function execAsyncKill(
  runtime: AsyncToolRuntime,
  args: Readonly<Record<string, unknown>>,
): Promise<AsyncToolPeekResult> {
  const handleId = typeof args.handleId === "string" ? args.handleId.trim() : "";
  if (!handleId) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "async_kill requires a 'handleId'");
  }
  const result = runtime.kill(handleId);
  if (!result) {
    throw new ApplicationError("AGENT_INVALID_INPUT", `Unknown handleId: ${handleId}`);
  }
  return result;
}
